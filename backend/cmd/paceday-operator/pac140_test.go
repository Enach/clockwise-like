package main

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/Enach/paceday/backend/storage"
)

// PAC-140 (SF-108): a failed count of the values left under the retired data
// key, or a failed sealed_count flush, stops the command with exit 1 and a
// message; the command never prints a count it did not read.

// rotateReady seeds plain values and converts them under data key 1.
func rotateReady(t *testing.T) *opEnv {
	t.Helper()
	e := newOpEnv(t)
	e.seedPlain()
	if code, _, stderr := e.run(e.keyed(), "convert", "--operator", "o", "--reason", "r"); code != exitOK {
		t.Fatalf("convert: %d %s", code, stderr)
	}
	return e
}

// countLine matches a printed count of the values left under a data key.
var countLine = regexp.MustCompile(`\d+ values left under data key`)

func lastAuditOutcome(t *testing.T, e *opEnv, action string) string {
	t.Helper()
	var outcome string
	if err := e.db.QueryRow(`SELECT details->>'outcome' FROM operator_audit WHERE action = $1 ORDER BY id DESC LIMIT 1`, action).Scan(&outcome); err != nil {
		t.Fatal(err)
	}
	return outcome
}

func TestPAC140_RotateCountFailureStops(t *testing.T) {
	e := rotateReady(t)
	InstallCountValuesUnder(t, func(dek int32) error {
		if dek != 1 {
			t.Errorf("count of data key %d, want 1", dek)
		}
		return errors.New("count refused")
	})
	code, out, stderr := e.run(e.keyed(), "rotate-data-key", "--operator", "o", "--reason", "r", "--retire", "1")
	if code != exitFailed {
		t.Fatalf("a failed count must exit 1, got %d\n%s\n%s", code, out, stderr)
	}
	if countLine.MatchString(out + stderr) {
		t.Fatalf("a count that was not read is printed:\n%s\n%s", out, stderr)
	}
	if !strings.Contains(stderr, "could not count the values left under data key 1") || !strings.Contains(stderr, "count refused") || !strings.Contains(stderr, "run the same command again") {
		t.Fatalf("stderr does not say the count failed:\n%s", stderr)
	}
	if !strings.Contains(out, "2 values re-encrypted") {
		t.Fatalf("the re-encryption that happened is not reported:\n%s", out)
	}
	if got := lastAuditOutcome(t, e, storage.AuditEncryptionRotate); got == "completed" {
		t.Fatalf("audit outcome %q after a failed count", got)
	}
	// With the seam gone the same command finds 0 values left and exits 0.
	countValuesUnderHook.p.Store(nil)
	code, out, stderr = e.run(e.keyed(), "rotate-data-key", "--operator", "o", "--reason", "again", "--retire", "1")
	if code != exitOK || !strings.Contains(out, "0 values re-encrypted; 0 values left under data key 1") {
		t.Fatalf("re-run: %d\n%s\n%s", code, out, stderr)
	}
}

func TestPAC140_RotateValuesLeftIsAFailure(t *testing.T) {
	e := rotateReady(t)
	// A value sealed under data key 1 after the pass (a straggler): the
	// count reads 1 and the command must not report success.
	InstallCountValuesUnder(t, func(int32) error {
		if _, err := e.db.Exec(`UPDATE settings SET webcal_url = 'pdenc:v1:1:AAAA'`); err != nil {
			t.Error(err)
		}
		return nil
	})
	code, out, stderr := e.run(e.keyed(), "rotate-data-key", "--operator", "o", "--reason", "r", "--retire", "1")
	if code != exitFailed {
		t.Fatalf("values left under the retired key must exit 1, got %d\n%s\n%s", code, out, stderr)
	}
	if !strings.Contains(stderr, "1 values left under data key 1") || !strings.Contains(stderr, "run the same command again") {
		t.Fatalf("stderr does not name the values left:\n%s", stderr)
	}
	if countLine.MatchString(out) {
		t.Fatalf("stdout reports values left as success:\n%s", out)
	}
}

func TestPAC140_FlushFailureStops(t *testing.T) {
	for _, sub := range []string{"convert", "rotate-data-key"} {
		t.Run(sub, func(t *testing.T) {
			e := newOpEnv(t)
			e.seedPlain()
			args := []string{"convert", "--operator", "o", "--reason", "r"}
			action := storage.AuditEncryptionConvert
			if sub == "rotate-data-key" {
				if code, _, stderr := e.run(e.keyed(), args...); code != exitOK {
					t.Fatalf("convert: %d %s", code, stderr)
				}
				args = []string{"rotate-data-key", "--operator", "o", "--reason", "r", "--retire", "1"}
				action = storage.AuditEncryptionRotate
			}
			var calls int
			InstallFlushSealCounts(t, func(got string) error {
				calls++
				if got != sub {
					t.Errorf("flush seam fired for %q, want %q", got, sub)
				}
				return errors.New("flush refused")
			})
			code, out, stderr := e.run(e.keyed(), args...)
			if calls != 1 {
				t.Fatalf("flush seam fired %d times", calls)
			}
			if code != exitFailed {
				t.Fatalf("a failed flush must exit 1, got %d\n%s\n%s", code, out, stderr)
			}
			if !strings.Contains(stderr, "seal counts not recorded") || !strings.Contains(stderr, "flush refused") {
				t.Fatalf("stderr does not say the flush failed:\n%s", stderr)
			}
			if got := lastAuditOutcome(t, e, action); got == "completed" {
				t.Fatalf("audit outcome %q after a failed flush", got)
			}
		})
	}
}

// The audit-table probe of a run refused below the encryption migration: a
// failed probe is reported and the audit row is still attempted (here the
// table exists, so the row is written).
func TestPAC140_AuditProbeFailureIsReported(t *testing.T) {
	e := newOpEnvAtEncryption(t)
	e.seedPlain()
	for _, args := range [][]string{
		{"convert", "--operator", "o", "--reason", "r"},
		{"decrypt-all", "--operator", "o", "--reason", "r"},
		{"downgrade-schema", "--operator", "o", "--reason", "r"},
	} {
		if code, _, stderr := e.run(e.keyed(), args...); code != exitOK {
			t.Fatalf("%s: %d %s", args[0], code, stderr)
		}
	}
	before := e.audits(storage.AuditEncryptionConvert)
	InstallAuditTableProbe(t, func() error { return errors.New("probe refused") })
	code, _, stderr := e.run(e.keyed(), "convert", "--operator", "o", "--reason", "r")
	if code != exitFailed || !strings.Contains(stderr, "applies no migration") {
		t.Fatalf("convert after downgrade: %d %s", code, stderr)
	}
	if !strings.Contains(stderr, "could not check whether operator_audit exists (probe refused)") {
		t.Fatalf("a failed probe is not reported: %s", stderr)
	}
	if e.audits(storage.AuditEncryptionConvert) != before+1 {
		t.Fatal("a failed probe skipped the audit row")
	}
}
