package main

// PAC-84 contract section 8: `paceday-operator encryption <subcommand>`.
// Output never contains a value, a ciphertext or a key byte; key ids and
// data-key ids are printed.

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/Enach/paceday/backend/config"
	"github.com/Enach/paceday/backend/internal/secretbox"
	"github.com/Enach/paceday/backend/storage"
)

// Exit codes (contract 8.1).
const (
	exitOK       = 0
	exitFailed   = 1
	exitUsage    = 2
	exitPlain    = 3
	exitKey      = 4
	exitLock     = 5
	exitBackends = 6
)

const encryptionUsage = "usage: paceday-operator encryption status [--check] | convert | rotate-data-key --retire <id> | decrypt-all | reset-lost-key --confirm-lost-key | downgrade-schema; mutating subcommands need --operator and --reason"

var subcommands = map[string]bool{
	"status": true, "convert": true, "rotate-data-key": true,
	"decrypt-all": true, "reset-lost-key": true, "downgrade-schema": true,
}

// encryptionRun is one invocation's flags and environment.
type encryptionRun struct {
	sub, operator, reason string
	check, confirm, empty bool
	batch                 int
	retire                int
	wait                  time.Duration
	getenv                func(string) string
	out, errw             io.Writer
	started               time.Time
	pollEvery             time.Duration
}

// openDB is storage.OpenAs; tests replace it.
var openDB = storage.OpenAs

func runEncryption(args []string, getenv func(string) string, out, errw io.Writer) int {
	if len(args) == 0 || !subcommands[args[0]] {
		fmt.Fprintln(errw, encryptionUsage)
		return exitUsage
	}
	r := &encryptionRun{sub: args[0], getenv: getenv, out: out, errw: errw, started: time.Now().UTC(), pollEvery: 2 * time.Second}
	fs := flag.NewFlagSet("encryption "+r.sub, flag.ContinueOnError)
	fs.SetOutput(errw)
	mutating := r.sub != "status"
	if mutating {
		fs.StringVar(&r.operator, "operator", "", "who runs the step (audited)")
		fs.StringVar(&r.reason, "reason", "", "why (audited)")
	}
	switch r.sub {
	case "status":
		fs.BoolVar(&r.check, "check", false, "exit 3 while any plain value remains")
	case "convert":
		fs.IntVar(&r.batch, "batch", 100, "rows per batch (1..1000)")
	case "rotate-data-key":
		fs.IntVar(&r.retire, "retire", 0, "the data key to retire and re-encrypt away from")
		fs.IntVar(&r.batch, "batch", 100, "rows per batch (1..1000)")
		fs.DurationVar(&r.wait, "wait", 5*time.Minute, "how long to wait for running backends to switch")
	case "decrypt-all":
		fs.BoolVar(&r.empty, "empty-undecryptable", false, "remove values that cannot be decrypted (lost-key end state) instead of stopping")
	case "reset-lost-key":
		fs.BoolVar(&r.confirm, "confirm-lost-key", false, "confirm the key is lost: every encrypted value is removed")
	}
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() > 0 {
		if err == nil {
			fmt.Fprintf(errw, "encryption %s: unexpected argument %q\n", r.sub, fs.Arg(0))
		}
		return exitUsage
	}
	if mutating && (strings.TrimSpace(r.operator) == "" || strings.TrimSpace(r.reason) == "") {
		fmt.Fprintf(errw, "encryption %s: --operator and --reason are required\n", r.sub)
		return exitUsage
	}
	if r.sub == "rotate-data-key" && r.retire <= 0 {
		fmt.Fprintln(errw, "encryption rotate-data-key: --retire <data key id> is required")
		return exitUsage
	}
	if r.sub == "reset-lost-key" && !r.confirm {
		fmt.Fprintln(errw, "encryption reset-lost-key: --confirm-lost-key is required (every encrypted value will be removed)")
		return exitUsage
	}
	if (r.sub == "convert" || r.sub == "rotate-data-key") && (r.batch < 1 || r.batch > 1000) {
		fmt.Fprintln(errw, "encryption: --batch must be between 1 and 1000")
		return exitUsage
	}
	dsn := getenv("DATABASE_URL")
	if dsn == "" {
		fmt.Fprintln(errw, "DATABASE_URL is required")
		return exitUsage
	}
	var cfg secretbox.KeyConfig
	switch r.sub {
	case "convert", "rotate-data-key", "decrypt-all", "reset-lost-key":
		c, err := config.DecideEncryptionKeys(getenv)
		if err != nil {
			fmt.Fprintln(errw, config.EncryptionRefusal("encryption: ", err))
			return exitKey
		}
		cfg = c
	}
	db, err := openDB(dsn, storage.OperatorApp(r.sub))
	if err != nil {
		fmt.Fprintln(errw, "encryption:", err)
		return exitFailed
	}
	defer db.Close()
	ctx := context.Background()
	if r.sub == "status" {
		return r.status(ctx, db)
	}
	lock, err := storage.TryMaintenanceLock(ctx, db)
	if err != nil {
		if errors.Is(err, storage.ErrLockHeld) {
			fmt.Fprintf(errw, "encryption %s: %v; try again after it ends\n", r.sub, err)
			return exitLock
		}
		fmt.Fprintln(errw, "encryption:", err)
		return exitFailed
	}
	defer lock.Release()
	switch r.sub {
	case "decrypt-all", "reset-lost-key", "downgrade-schema":
		live, err := storage.LiveHeartbeats(ctx, db)
		if err != nil {
			fmt.Fprintln(errw, "encryption:", err)
			return exitFailed
		}
		if len(live) > 0 {
			fmt.Fprintf(errw, "encryption %s: %d backend process(es) wrote a heartbeat in the last 90 seconds (e.g. instance %s on %q); stop every backend first (docs/runbook.md, \"Encryption key - stop the backend first\")\n",
				r.sub, len(live), live[0].InstanceID, live[0].Hostname)
			return exitBackends
		}
	}
	switch r.sub {
	case "convert":
		return r.convert(ctx, db, cfg)
	case "rotate-data-key":
		return r.rotate(ctx, db, cfg)
	case "decrypt-all":
		return r.decryptAll(ctx, db, cfg)
	case "reset-lost-key":
		return r.resetLostKey(ctx, db, cfg)
	default:
		return r.downgrade(ctx, db)
	}
}

func (r *encryptionRun) keyIDs(cfg secretbox.KeyConfig) *storage.AuditKeyIDs {
	prev := cfg.PreviousIDs()
	if prev == nil {
		prev = []string{}
	}
	return &storage.AuditKeyIDs{Active: cfg.Active.ID, Previous: prev}
}

// audit writes the run's one operator_audit row; a failure to write it
// makes the run fail.
func (r *encryptionRun) audit(ctx context.Context, db *sql.DB, action string, d storage.AuditDetails, code int) int {
	d.StartedAt = r.started.Format(time.RFC3339)
	d.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	if err := storage.WriteEncryptionAudit(ctx, db, action, r.operator, r.reason, d); err != nil {
		fmt.Fprintln(r.errw, "encryption: audit record not written:", err)
		return exitFailed
	}
	return code
}

func outcome(code int, partial bool) string {
	switch {
	case code != exitOK && !partial:
		return "failed"
	case partial || code != exitOK:
		return "partial"
	}
	return "completed"
}

// operatorKeyring unwraps the stored data keys with the operator's keys
// for convert and rotate-data-key: exit 4 unless the active key wraps the
// active data key (K-6). On a database with no active data key it creates
// one, as a backend's first start does.
func (r *encryptionRun) operatorKeyring(ctx context.Context, db *sql.DB, cfg secretbox.KeyConfig) (*secretbox.Keyring, int) {
	deks, err := storage.LoadDataKeys(ctx, db)
	if err != nil {
		fmt.Fprintln(r.errw, "encryption:", err)
		return nil, exitFailed
	}
	active := false
	for _, w := range deks {
		if w.State == "active" {
			active = true
			if w.KEKID != cfg.Active.ID {
				fmt.Fprintf(r.errw, "encryption: PACEDAY_ENCRYPTION_KEY is key %q but the active data key %d is wrapped by key %q; run this command with the key the backends use %s\n",
					cfg.Active.ID, w.ID, w.KEKID, secretbox.RunbookRef)
				return nil, exitKey
			}
		}
	}
	if !active {
		load, err := storage.PrepareKeyring(ctx, db, cfg, true)
		if err != nil {
			return nil, r.keyFailure(err)
		}
		return load.Keyring, exitOK
	}
	k, err := secretbox.NewKeyring(cfg, deks)
	if err != nil {
		return nil, r.keyFailure(err)
	}
	return k, exitOK
}

func (r *encryptionRun) keyFailure(err error) int {
	if errors.Is(err, secretbox.ErrWrongKey) || errors.Is(err, secretbox.ErrUnknownKEK) || errors.Is(err, secretbox.ErrNoActiveDEK) {
		fmt.Fprintln(r.errw, config.EncryptionRefusal("encryption: ", err))
		return exitKey
	}
	fmt.Fprintln(r.errw, "encryption:", err)
	return exitFailed
}

func (r *encryptionRun) printPass(res *storage.PassResult) {
	cols := make([]string, 0, len(res.Counts))
	for c := range res.Counts {
		cols = append(cols, c)
	}
	sort.Strings(cols)
	for _, c := range cols {
		verbs := make([]string, 0)
		for v, n := range res.Counts[c] {
			verbs = append(verbs, fmt.Sprintf("%s %d", v, n))
		}
		sort.Strings(verbs)
		fmt.Fprintf(r.out, "  %-42s %s\n", c, strings.Join(verbs, ", "))
	}
	for _, f := range res.Failures {
		fmt.Fprintf(r.errw, "  not processed: %s row=%s dek=%s reason=%s\n", f.Column, f.Row, f.DEK, f.Reason)
	}
	if res.Partial {
		fmt.Fprintf(r.errw, "  %d values left (rows kept changing); run the command again\n", res.Remaining)
	}
}

func (r *encryptionRun) convert(ctx context.Context, db *sql.DB, cfg secretbox.KeyConfig) int {
	k, code := r.operatorKeyring(ctx, db, cfg)
	if code != exitOK {
		return code
	}
	res, err := storage.RunPass(ctx, db, storage.PassOptions{Kind: storage.PassConvert, Batch: r.batch, Keyring: k})
	_ = storage.FlushSealCounts(ctx, db, k.TakeSealCounts())
	code = exitOK
	if err != nil {
		fmt.Fprintln(r.errw, "encryption convert:", err)
		code = exitFailed
	}
	if res != nil {
		r.printPass(res)
		if res.Partial || len(res.Failures) > 0 {
			code = exitFailed
		}
	} else {
		res = &storage.PassResult{}
	}
	fmt.Fprintf(r.out, "encryption convert: %d values encrypted\n", res.Total("encrypted"))
	return r.audit(ctx, db, storage.AuditEncryptionConvert, storage.AuditDetails{
		Outcome: outcome(code, res.Partial || len(res.Failures) > 0), KeyIDs: r.keyIDs(cfg), Counts: res.Counts,
	}, code)
}

func (r *encryptionRun) rotate(ctx context.Context, db *sql.DB, cfg secretbox.KeyConfig) int {
	if _, code := r.operatorKeyring(ctx, db, cfg); code != exitOK {
		return code
	}
	retire := int32(r.retire)
	details := storage.AuditDetails{KeyIDs: r.keyIDs(cfg), DataKeys: &storage.AuditDataKeys{}}
	newID, err := storage.RotateStep1(ctx, db, cfg, retire, false)
	if err != nil {
		fmt.Fprintln(r.errw, "encryption rotate-data-key:", err)
		details.Outcome = "failed"
		return r.audit(ctx, db, storage.AuditEncryptionRotate, details, exitFailed)
	}
	if newID != 0 {
		details.DataKeys.Created = []int32{newID}
		details.DataKeys.Retired = []int32{retire}
		fmt.Fprintf(r.out, "data key %d retired; data key %d is now active\n", retire, newID)
	}
	active, _, _, err := storage.ActiveDataKey(ctx, db)
	if err != nil {
		fmt.Fprintln(r.errw, "encryption rotate-data-key:", err)
		details.Outcome = "failed"
		return r.audit(ctx, db, storage.AuditEncryptionRotate, details, exitFailed)
	}
	// Step 2: every live backend seals with the new data key.
	deadline := time.Now().Add(r.wait)
	for {
		live, err := storage.LiveHeartbeats(ctx, db)
		if err != nil {
			fmt.Fprintln(r.errw, "encryption rotate-data-key:", err)
			details.Outcome = "failed"
			return r.audit(ctx, db, storage.AuditEncryptionRotate, details, exitFailed)
		}
		behind := 0
		for _, h := range live {
			if h.ActiveDEK == nil || *h.ActiveDEK != active {
				behind++
			}
		}
		if behind == 0 {
			break
		}
		if time.Now().After(deadline) {
			fmt.Fprintf(r.errw, "encryption rotate-data-key: %d backend(s) still seal with another data key after %s; run the same command again\n", behind, r.wait)
			details.Outcome = "partial"
			return r.audit(ctx, db, storage.AuditEncryptionRotate, details, exitFailed)
		}
		time.Sleep(r.pollEvery)
	}
	// Step 3: re-seal every value under the retired key.
	k, code := r.operatorKeyring(ctx, db, cfg)
	if code != exitOK {
		details.Outcome = "failed"
		return r.audit(ctx, db, storage.AuditEncryptionRotate, details, exitFailed)
	}
	res, err := storage.RunPass(ctx, db, storage.PassOptions{Kind: storage.PassRotate, Batch: r.batch, Keyring: k, Retire: retire})
	_ = storage.FlushSealCounts(ctx, db, k.TakeSealCounts())
	code = exitOK
	if err != nil {
		fmt.Fprintln(r.errw, "encryption rotate-data-key:", err)
		code = exitFailed
	}
	if res == nil {
		res = &storage.PassResult{}
	}
	r.printPass(res)
	if res.Partial || len(res.Failures) > 0 {
		code = exitFailed
	}
	left, _ := storage.CountValuesUnder(ctx, db, retire)
	fmt.Fprintf(r.out, "encryption rotate-data-key: %d values re-encrypted; %d values left under data key %d\n", res.Total("reencrypted"), left, retire)
	details.Outcome = outcome(code, res.Partial || len(res.Failures) > 0)
	details.Counts = res.Counts
	return r.audit(ctx, db, storage.AuditEncryptionRotate, details, code)
}

func (r *encryptionRun) decryptAll(ctx context.Context, db *sql.DB, cfg secretbox.KeyConfig) int {
	deks, err := storage.LoadDataKeys(ctx, db)
	if err != nil {
		fmt.Fprintln(r.errw, "encryption:", err)
		return exitFailed
	}
	var k *secretbox.Keyring
	if len(deks) > 0 {
		if k, err = secretbox.NewKeyring(cfg, deks); err != nil {
			return r.keyFailure(err)
		}
	}
	details := storage.AuditDetails{KeyIDs: r.keyIDs(cfg), DataKeys: &storage.AuditDataKeys{}}
	res, err := storage.RunPass(ctx, db, storage.PassOptions{Kind: storage.PassDecrypt, Batch: 100, Keyring: k, EmptyUndecryptable: r.empty})
	code := exitOK
	if err != nil {
		fmt.Fprintln(r.errw, "encryption decrypt-all:", err)
		code = exitFailed
	}
	if res == nil {
		res = &storage.PassResult{}
	}
	r.printPass(res)
	if res.Partial || len(res.Failures) > 0 {
		code = exitFailed
	}
	if code == exitOK {
		removed, ok, err := storage.DeleteAllDataKeysIfUnused(ctx, db)
		switch {
		case err != nil:
			fmt.Fprintln(r.errw, "encryption decrypt-all:", err)
			code = exitFailed
		case !ok:
			fmt.Fprintln(r.errw, "encryption decrypt-all: encrypted values remain (written meanwhile?); data keys kept; run it again")
			code = exitFailed
		default:
			details.DataKeys.Removed = removed
			fmt.Fprintf(r.out, "data keys removed: %v\n", removed)
		}
	} else {
		fmt.Fprintln(r.errw, "encryption decrypt-all: data keys kept (values remain encrypted); fix them or use --empty-undecryptable")
	}
	fmt.Fprintf(r.out, "encryption decrypt-all: %d values decrypted, %d emptied\n", res.Total("decrypted"), res.Total("emptied"))
	details.Outcome = outcome(code, res.Partial || len(res.Failures) > 0)
	details.Counts = res.Counts
	return r.audit(ctx, db, storage.AuditEncryptionDecrypt, details, code)
}

func (r *encryptionRun) resetLostKey(ctx context.Context, db *sql.DB, cfg secretbox.KeyConfig) int {
	deks, err := storage.LoadDataKeys(ctx, db)
	if err != nil {
		fmt.Fprintln(r.errw, "encryption:", err)
		return exitFailed
	}
	if w, k, ok := secretbox.OpensAny(cfg, deks); ok {
		fmt.Fprintf(r.errw, "encryption: key %q (%s) opens data key %d: the key is not lost; nothing was changed %s\n", k.ID, k.Var, w.ID, secretbox.RunbookRef)
		return exitKey
	}
	details := storage.AuditDetails{KeyIDs: r.keyIDs(cfg), DataKeys: &storage.AuditDataKeys{}}
	res, removed, created, err := storage.ResetLostKey(ctx, db, cfg)
	if err != nil {
		fmt.Fprintln(r.errw, "encryption reset-lost-key:", err)
		details.Outcome = "failed"
		return r.audit(ctx, db, storage.AuditEncryptionResetLost, details, exitFailed)
	}
	r.printPass(res)
	details.DataKeys.Removed = removed
	details.DataKeys.Created = []int32{created}
	details.Counts = res.Counts
	details.Outcome = "completed"
	fmt.Fprintf(r.out, "encryption reset-lost-key: data keys removed %v; data key %d created under key %q; users reconnect the removed connections\n", removed, created, cfg.Active.ID)
	return r.audit(ctx, db, storage.AuditEncryptionResetLost, details, exitOK)
}

func (r *encryptionRun) downgrade(ctx context.Context, db *sql.DB) int {
	from, to, err := storage.DowngradeSchema(ctx, db)
	details := storage.AuditDetails{}
	if errors.Is(err, storage.ErrDowngradeRefused) {
		// Nothing changed: refused before the down step (contract 7.2).
		fmt.Fprintln(r.errw, err)
		return exitFailed
	}
	var ver *storage.ErrDowngradeVersion
	if errors.As(err, &ver) {
		fmt.Fprintln(r.errw, "encryption downgrade-schema:", err)
		return exitFailed
	}
	if err != nil {
		fmt.Fprintln(r.errw, "encryption downgrade-schema:", err)
		details.Outcome = "failed"
		return r.audit(ctx, db, storage.AuditEncryptionDowngrade, details, exitFailed)
	}
	fmt.Fprintf(r.out, "encryption downgrade-schema: schema version %d -> %d; start the previous release now (never this one: it would encrypt again)\n", from, to)
	details.Outcome = "completed"
	return r.audit(ctx, db, storage.AuditEncryptionDowngrade, details, exitOK)
}

func (r *encryptionRun) status(ctx context.Context, db *sql.DB) int {
	st, err := storage.ReadEncryptionStatus(ctx, db)
	if err != nil {
		fmt.Fprintln(r.errw, "encryption status:", err)
		return exitFailed
	}
	now := time.Now()
	fmt.Fprintf(r.out, "%-42s %8s %8s %10s  %s\n", "column", "empty", "plain", "encrypted", "encrypted per data key")
	for _, c := range st.Columns {
		keys := make([]string, 0, len(c.ByDEK))
		for id, n := range c.ByDEK {
			keys = append(keys, fmt.Sprintf("%s:%d", id, n))
		}
		sort.Strings(keys)
		fmt.Fprintf(r.out, "%-42s %8d %8d %10d  %s\n", c.Name, c.Empty, c.Plain, c.Encrypted, strings.Join(keys, " "))
	}
	fmt.Fprintln(r.out, "data keys:")
	if len(st.DataKeys) == 0 {
		fmt.Fprintln(r.out, "  none")
	}
	for _, d := range st.DataKeys {
		retired := "-"
		if d.RetiredAt != nil {
			retired = d.RetiredAt.UTC().Format(time.RFC3339)
		}
		due := ""
		if d.RotateDue(now) {
			due = "  rotate due"
		}
		fmt.Fprintf(r.out, "  %d %-7s wrapped by key %q  created %s (%d days)  wrapped_at %s  retired_at %s  sealed_count %d  values %d%s\n",
			d.ID, d.State, d.KEKID, d.CreatedAt.UTC().Format(time.RFC3339), int(now.Sub(d.CreatedAt).Hours()/24),
			d.WrappedAt.UTC().Format(time.RFC3339), retired, d.SealedCount, d.Values, due)
	}
	fmt.Fprintln(r.out, "backends (heartbeat in the last 90 s):")
	if len(st.Heartbeats) == 0 {
		fmt.Fprintln(r.out, "  none")
	}
	for _, h := range st.Heartbeats {
		dek := "-"
		if h.ActiveDEK != nil {
			dek = fmt.Sprint(*h.ActiveDEK)
		}
		fmt.Fprintf(r.out, "  instance %s host %q active_dek %s seen %s\n", h.InstanceID, h.Hostname, dek, h.SeenAt.UTC().Format(time.RFC3339))
	}
	plain := st.PlainTotal()
	fmt.Fprintf(r.out, "plain values: %d\n", plain)
	if r.check && plain > 0 {
		return exitPlain
	}
	return exitOK
}
