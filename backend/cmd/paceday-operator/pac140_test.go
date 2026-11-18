package main

import (
	"errors"
	"fmt"
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
	uninstall := InstallCountValuesUnder(t, func(dek int32) error {
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
	uninstall()
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

// flushCase seeds a run of sub whose seals land on one data key: convert
// seals 2 values under data key 1; rotate-data-key (after a convert) seals
// them under data key 2. It returns the args, the audit action and that key.
func flushCase(t *testing.T, e *opEnv, sub string) ([]string, string, int32) {
	t.Helper()
	e.seedPlain()
	args := []string{"convert", "--operator", "o", "--reason", "r"}
	if sub == "convert" {
		return args, storage.AuditEncryptionConvert, 1
	}
	if code, _, stderr := e.run(e.keyed(), args...); code != exitOK {
		t.Fatalf("convert: %d %s", code, stderr)
	}
	return []string{"rotate-data-key", "--operator", "o", "--reason", "r", "--retire", "1"}, storage.AuditEncryptionRotate, 2
}

func (e *opEnv) sealed(id int32) int64 {
	e.t.Helper()
	var n int64
	if err := e.db.QueryRow(`SELECT sealed_count FROM encryption_data_keys WHERE id = $1`, id).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// A flush that fails once is retried, and the run succeeds with the seals
// recorded exactly once.
func TestPAC140_FlushRetrySucceeds(t *testing.T) {
	for _, sub := range []string{"convert", "rotate-data-key"} {
		t.Run(sub, func(t *testing.T) {
			e := newOpEnv(t)
			args, _, dek := flushCase(t, e, sub)
			var calls int
			InstallFlushSealCounts(t, func(got string, id int32) error {
				calls++
				if got != sub || id != dek {
					t.Errorf("flush seam fired for %q data key %d, want %q data key %d", got, id, sub, dek)
				}
				if calls == 1 {
					return errors.New("flush refused once")
				}
				return nil
			})
			code, out, stderr := e.run(e.keyed(), args...)
			if code != exitOK || calls != 2 {
				t.Fatalf("a flush that succeeds on retry: exit %d, %d attempts: %s %s", code, calls, out, stderr)
			}
			if strings.Contains(stderr, "seal counts not recorded") {
				t.Fatalf("a recorded flush is reported as failed: %s", stderr)
			}
			if got := e.sealed(dek); got != 2 {
				t.Fatalf("sealed_count of data key %d = %d, want 2", dek, got)
			}
		})
	}
}

// A flush that fails twice ends the run with exit 1 and prints, per data
// key, the missing count and the statement that records it; running that
// statement restores sealed_count, and a re-run does not.
func TestPAC140_FlushFailureStops(t *testing.T) {
	for _, sub := range []string{"convert", "rotate-data-key"} {
		t.Run(sub, func(t *testing.T) {
			e := newOpEnv(t)
			args, action, dek := flushCase(t, e, sub)
			var calls int
			uninstall := InstallFlushSealCounts(t, func(got string, id int32) error {
				calls++
				if got != sub || id != dek {
					t.Errorf("flush seam fired for %q data key %d, want %q data key %d", got, id, sub, dek)
				}
				return errors.New("flush refused")
			})
			code, out, stderr := e.run(e.keyed(), args...)
			if calls != 2 {
				t.Fatalf("flush attempted %d times, want 2 (one retry)", calls)
			}
			if code != exitFailed {
				t.Fatalf("a failed flush must exit 1, got %d: %s %s", code, out, stderr)
			}
			stmt := fmt.Sprintf("UPDATE encryption_data_keys SET sealed_count = sealed_count + 2 WHERE id = %d;", dek)
			if !strings.Contains(stderr, "seal counts not recorded after a retry (flush refused)") ||
				!strings.Contains(stderr, fmt.Sprintf("data key %d: 2 seals missing: %s", dek, stmt)) ||
				!strings.Contains(stderr, "a re-run does not record them") {
				t.Fatalf("stderr does not name the missing counts: %s", stderr)
			}
			if strings.Contains(stderr, "run the same command again") {
				t.Fatalf("a failed flush advises a re-run: %s", stderr)
			}
			if got := lastAuditOutcome(t, e, action); got == "completed" {
				t.Fatalf("audit outcome %q after a failed flush", got)
			}
			if got := e.sealed(dek); got != 0 {
				t.Fatalf("sealed_count of data key %d = %d after a refused flush, want 0", dek, got)
			}
			uninstall()
			// The runbook's step: run the printed statement.
			if _, err := e.db.Exec(strings.TrimSuffix(stmt, ";")); err != nil {
				t.Fatal(err)
			}
			if got := e.sealed(dek); got != 2 {
				t.Fatalf("sealed_count of data key %d = %d after the printed statement, want 2", dek, got)
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
