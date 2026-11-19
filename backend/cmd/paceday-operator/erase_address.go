package main

// PAC-134 (spec docs/specs/PAC-134.md; contract contracts/features/PAC-134.md
// section 2): `paceday-operator erase-address`, the GDPR erasure of a third
// party's address from other people's data. The address comes on standard
// input only; nothing this command prints, logs or stores holds it, a name,
// a title, a note, a slug, a digest of the address or an erased row's id.
// Errors are reported by class, never by the driver's message.

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// exitAccountHeld: an account holds the address and the erasure was not
// acknowledged (contract md §2.4).
const exitAccountHeld = 3

const eraseAddressUsage = "usage: paceday-operator erase-address -operator <name> -reason <text> [-dry-run] [-account-held-acknowledged] [-max-duration 5s] < address"

// Bounds of -max-duration (contract md §7, revision 2 note 8).
const (
	eraseMinDuration     = time.Second
	eraseMaxDuration     = 8 * time.Second
	eraseDefaultDuration = 5 * time.Second
	eraseMaxInput        = 4096
)

// eraseInput is one parsed invocation (contract md §2.1).
type eraseInput struct {
	operator, reason string
	dryRun, ack      bool
	maxDuration      time.Duration
	address          string // t: trimmed, letter case kept
}

// usageError is an input refusal: exit 2, one fixed line, no connection.
type usageError string

func (u usageError) Error() string { return "erase-address: " + string(u) + "; " + eraseAddressUsage }

// parseEraseInput checks flags, then standard input, then validity, then the
// address in the operator name or reason, in that order (contract md §2.1),
// before any database connection.
func parseEraseInput(args []string, getenv func(string) string, stdin io.Reader) (eraseInput, error) {
	var in eraseInput
	fs := flag.NewFlagSet("erase-address", flag.ContinueOnError)
	// The flag set's own output is discarded: it echoes an unknown flag,
	// which could be the address (-pat@x.example).
	fs.SetOutput(io.Discard)
	fs.StringVar(&in.operator, "operator", "", "who runs the erasure (audited)")
	fs.StringVar(&in.reason, "reason", "", "the request's ticket reference (audited)")
	fs.BoolVar(&in.dryRun, "dry-run", false, "count only; change nothing")
	fs.BoolVar(&in.ack, "account-held-acknowledged", false, "erase the copies in other people's data although an account holds the address")
	fs.DurationVar(&in.maxDuration, "max-duration", eraseDefaultDuration, "bound of the whole run, 1s to 8s")
	if err := fs.Parse(args); err != nil {
		return in, usageError("invalid flags")
	}
	if fs.NArg() > 0 {
		return in, usageError("arguments are not accepted; give the address on standard input")
	}
	if strings.TrimSpace(in.operator) == "" {
		return in, usageError("-operator is required")
	}
	if strings.TrimSpace(in.reason) == "" {
		return in, usageError("-reason is required")
	}
	if in.maxDuration < eraseMinDuration || in.maxDuration > eraseMaxDuration {
		return in, usageError("-max-duration must be between 1s and 8s")
	}
	if getenv("DATABASE_URL") == "" {
		return in, usageError("DATABASE_URL is required")
	}

	raw, err := io.ReadAll(io.LimitReader(stdin, eraseMaxInput+1))
	if err != nil {
		return in, usageError("standard input could not be read")
	}
	if len(raw) > eraseMaxInput {
		return in, usageError("standard input is longer than 4096 bytes")
	}
	if !utf8.Valid(raw) || strings.ContainsRune(string(raw), 0) {
		return in, usageError("standard input is not valid UTF-8 text")
	}
	line, rest, _ := strings.Cut(string(raw), "\n")
	if storage.TrimAddressSpace(rest) != "" {
		return in, usageError("more than one line on standard input")
	}
	t := storage.TrimAddressSpace(line)
	if t == "" {
		return in, usageError("no address on standard input")
	}
	l, ok := storage.ValidAddress(t)
	if !ok {
		return in, usageError("not a valid address")
	}
	if containsAddress(in.operator, t, l) || containsAddress(in.reason, t, l) {
		return in, usageError("the operator name and the reason must not contain the address")
	}
	in.address = t
	return in, nil
}

// containsAddress is the refusal of contract md §2.1 item 4 (D-6): f holds
// the address under Go lowercasing, or under Unicode simple folding (which
// covers what a libc lower() equates on top of it).
func containsAddress(f, t, l string) bool {
	return strings.Contains(strings.ToLower(f), l) || strings.Contains(simpleFold(f), simpleFold(t))
}

// simpleFold maps every rune to the smallest rune of its unicode.SimpleFold
// orbit.
func simpleFold(s string) string {
	var b strings.Builder
	for _, r := range s {
		m := r
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			if f < m {
				m = f
			}
		}
		b.WriteRune(m)
	}
	return b.String()
}

// runEraseAddress is the command. ctx is cancelled on SIGINT/SIGTERM by
// main (stage-4 amendment N-1), which cancels the in-flight statement and
// rolls the run back.
func runEraseAddress(ctx context.Context, args []string, getenv func(string) string, stdin io.Reader, out, errw io.Writer) int {
	in, err := parseEraseInput(args, getenv, stdin)
	if err != nil {
		fmt.Fprintln(errw, err.Error())
		return exitUsage
	}
	dsn := getenv("DATABASE_URL")
	fail := func(class string) int {
		fmt.Fprintf(errw, "erase-address: failed: %s; no data changed\n", class)
		return exitFailed
	}

	want, err := storage.EraseAddressMigrationVersion()
	if err != nil {
		return fail("unclassified")
	}
	if !in.dryRun {
		// As PAC-71's subcommands: pending migrations only while the app is
		// not serving (PAC-116). The dry run never migrates.
		_, err := storage.MigrateWhenStopped(ctx, dsn, storage.MigrateOptions{Caller: storage.EraseAddressApp})
		var refusal *storage.MigrationRefusal
		switch {
		case errors.As(err, &refusal):
			return fail("migration refused: " + refusal.Error())
		case err != nil:
			return fail(connectClass(err))
		}
	}
	db, err := storage.OpenNoMigrateDescribeExec(dsn, storage.EraseAddressApp)
	if err != nil {
		return fail(connectClass(err))
	}
	defer db.Close()
	present, class := erasePresence(ctx, db)
	if present == nil {
		return fail(class)
	}
	defer present.Close()
	version, dirty, ok, err := storage.SchemaVersion(ctx, db)
	switch {
	case err != nil:
		return fail(eraseErrorClass(err, in.maxDuration))
	case dirty:
		return fail("schema dirty")
	case !ok || version < want:
		fmt.Fprintf(errw, "erase-address: failed: schema too old (at %d, needs %d, PAC-134); no data changed and no migration applied\n", version, want)
		return exitFailed
	}

	res, err := storage.EraseAddress(ctx, db, storage.EraseAddressOptions{
		Address: in.address, DryRun: in.dryRun, AccountHeldAcknowledged: in.ack,
		MaxDuration: in.maxDuration, Run: storage.OperatorRun{Operator: in.operator, Reason: in.reason},
	})
	if errors.Is(err, storage.ErrEraseAccountHeld) && res != nil {
		fmt.Fprintln(out, accountsLine(res))
		fmt.Fprintln(errw, `erase-address: an account holds this address (ids above); nothing changed and no record written; to erase the copies in other people's data rerun with -account-held-acknowledged (runbook "Erasing an address")`)
		return exitAccountHeld
	}
	if err != nil {
		return fail(eraseErrorClass(err, in.maxDuration))
	}
	printEraseResult(out, res)
	if len(res.Accounts) > 0 && !in.ack {
		fmt.Fprintln(errw, "erase-address: an account holds this address; the erasure needs -account-held-acknowledged")
		return exitAccountHeld
	}
	return exitOK
}

// erasePresence is holdPresence with class-only errors: it pins a connection
// for the whole run (PAC-116 contract 4.5) and refuses while a migrator holds
// the maintenance lock.
func erasePresence(ctx context.Context, db *sql.DB) (*sql.Conn, string) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, connectClass(err)
	}
	holder, err := storage.MaintenanceHolder(ctx, conn)
	if err != nil {
		_ = conn.Close()
		return nil, engine.ErasureErrorClass(err)
	}
	if holder.Migrating() {
		_ = conn.Close()
		return nil, "maintenance lock held by a migrator"
	}
	return conn, ""
}

// connectClass classes an error from opening the database.
func connectClass(err error) string {
	var ce *pgconn.ConnectError
	if errors.As(err, &ce) {
		return "database unreachable"
	}
	if c := engine.ErasureErrorClass(err); c != "unclassified" {
		return c
	}
	return "database unreachable"
}

// eraseErrorClass is the class of contract md §2.6: never the driver's
// message, which can repeat row data.
func eraseErrorClass(err error, bound time.Duration) string {
	var pg *pgconn.PgError
	switch {
	case errors.Is(err, storage.ErrEraseBoundExceeded):
		return fmt.Sprintf("duration bound exceeded (%s)", bound)
	case errors.Is(err, storage.ErrEraseStatementLogging):
		return "statement logging could record the address; see the runbook"
	case errors.Is(err, context.Canceled):
		return "interrupted"
	case errors.As(err, &pg) && pg.Code == "55P03":
		return "lock wait timeout"
	case errors.As(err, &pg) && pg.Code == "40P01":
		return "deadlock"
	case errors.Is(err, sql.ErrConnDone), errors.Is(err, driver.ErrBadConn):
		return "connection closed"
	}
	return engine.ErasureErrorClass(err)
}

// accountsLine is the accounts= line of contract md §2.5.
func accountsLine(res *storage.EraseAddressResult) string {
	if len(res.Accounts) == 0 {
		return "accounts=none"
	}
	ids := make([]string, len(res.Accounts))
	for i, id := range res.Accounts {
		ids[i] = id.String()
	}
	return fmt.Sprintf("accounts=%s acknowledged=%t", strings.Join(ids, ","), res.Acknowledged)
}

// printEraseResult prints the key=value lines of contract md §2.5: counts,
// account ids and the record's id only.
func printEraseResult(out io.Writer, res *storage.EraseAddressResult) {
	count := "erased"
	if res.DryRun {
		count = "would_erase"
		fmt.Fprintln(out, "erase-address: dry run, no data changed")
	}
	fmt.Fprintln(out, accountsLine(res))
	var total, kept int64
	for _, p := range res.Places {
		fmt.Fprintf(out, "place=%s %s=%d kept_account_own=%d\n", p.Name, count, p.Erased, p.KeptAccountOwn)
		total += p.Erased
		kept += p.KeptAccountOwn
	}
	fmt.Fprintf(out, "place=operator_audit kept=%d\n", res.KeptOperatorRecords)
	fmt.Fprintf(out, "total %s=%d kept_account_own=%d kept_operator_records=%d\n", count, total, kept, res.KeptOperatorRecords)
	if res.AuditID != uuid.Nil {
		fmt.Fprintf(out, "audit=%s\n", res.AuditID)
	}
}
