// Command paceday-operator runs PAC-71's operator procedure on the server
// (spec docs/specs/PAC-71.md §3 "Operator procedure"; contract §9). It has no
// HTTP surface: only the deployment's operator, with direct access to the
// server and its database, can run it. Every run is audited (operator_audit).
//
//	paceday-operator free-address -account <uuid> -operator <name> -reason <text> [-squatter]
//	paceday-operator reassign     -account <uuid> -identity google:<sub>|microsoft:<tid>:<oid>
//	                              -evidence authoritative|mailbox -operator <name> -reason <text>
//	                              [-keep-calendar-credentials] [-remove slack,notion,zoom,ai-keys,external]
//	paceday-operator advance-all  -operator <name> -reason <text>
//	paceday-operator encryption status|convert|rotate-data-key|decrypt-all|reset-lost-key|downgrade-schema [flags]
//	paceday-operator booker-erasure status [--check]
//
// (encryption: PAC-84, contracts/features/PAC-84.md section 8; booker-erasure:
// PAC-40, contracts/features/PAC-40.md section 5. Both open the database
// themselves, without migrating, with their own application_name and exit
// codes; booker-erasure is read-only and not audited.)
// DATABASE_URL names the database. Pending migrations are applied first, only
// while the app is not serving (PAC-116: the migrate-when-stopped procedure;
// otherwise the command refuses with exit 1 and changes nothing).
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "encryption" {
		os.Exit(runEncryption(os.Args[2:], os.Getenv, os.Stdout, os.Stderr))
	}
	// PAC-40 (K4-5): read-only, dispatched before the PAC-71 path below,
	// which applies pending migrations.
	if len(os.Args) > 1 && os.Args[1] == "booker-erasure" {
		os.Exit(runBookerErasure(os.Args[2:], os.Getenv, os.Stdout, os.Stderr, time.Now))
	}
	os.Exit(runPAC71(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

// runPAC71 runs one of PAC-71's subcommands: DATABASE_URL and the flags are
// checked first (exit 2), then pending migrations go through the
// migrate-when-stopped procedure (PAC-116 contract 3; a refusal is exit 1),
// then the pool is opened and, on a connection kept open until the command
// ends, the maintenance lock's holder is read: a migrator at work is exit 1
// (contract 4.5). Then the command runs (exit 0, or 1 on failure).
func runPAC71(args []string, getenv func(string) string, out, errw io.Writer) int {
	dsn := getenv("DATABASE_URL")
	if dsn == "" {
		fmt.Fprintln(errw, "DATABASE_URL is required")
		return 2
	}
	cmd, err := parseCommand(args, out)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 2
	}
	// PAC-84 contract 4.3: every process names itself in application_name;
	// PAC-71's subcommands are "paceday-operator <subcommand>".
	app := "paceday-operator " + args[0]
	ctx := context.Background()
	res, err := storage.MigrateWhenStopped(ctx, dsn, storage.MigrateOptions{Caller: app})
	var refusal *storage.MigrationRefusal
	switch {
	case errors.As(err, &refusal):
		fmt.Fprintf(errw, "%s: %v\n", app, err)
		return 1
	case err != nil:
		fmt.Fprintln(errw, "run migrations:", err)
		return 1
	case res.Applied:
		fmt.Fprintf(errw, "%s: migrations applied, schema version %s -> %d\n", app, fromVersion(res), res.To)
	}
	db, err := storage.OpenNoMigrate(dsn, app)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	defer db.Close()
	present, code := holdPresence(context.Background(), db, app, errw)
	if present == nil {
		return code
	}
	defer present.Close()
	if err := cmd(ctx, db); err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	return 0
}

// holdPresence is the operator's half of PAC-116's write-then-check
// (contract 4.5): it pins a connection, which stays open until the command
// ends, so a migrator's serving check sees this process, and reads the
// maintenance lock's holder on it. On a migrator at work it reports the
// lock-held text and returns nil with exit 1.
func holdPresence(ctx context.Context, db *sql.DB, prefix string, errw io.Writer) (*sql.Conn, int) {
	conn, err := db.Conn(ctx)
	if err != nil {
		fmt.Fprintln(errw, err)
		return nil, 1
	}
	holder, err := storage.MaintenanceHolder(ctx, conn)
	if err != nil {
		_ = conn.Close()
		fmt.Fprintln(errw, err)
		return nil, 1
	}
	if holder.Migrating() {
		_ = conn.Close()
		fmt.Fprintf(errw, "%s: %s\n", prefix, storage.MigratingHolderText(holder))
		return nil, 1
	}
	return conn, 0
}

// fromVersion prints the version a migration run started from.
func fromVersion(res storage.MigrateResult) string {
	if !res.FromPresent {
		return "none"
	}
	return fmt.Sprint(res.From)
}

const usage = "usage: paceday-operator free-address|reassign|advance-all|encryption|booker-erasure [flags] (see -h on each; encryption: docs/runbook.md, \"Encryption key\"; booker-erasure: \"Booker erasure (PAC-40)\")"

// run executes one operator command against db and reports on out.
func run(ctx context.Context, db *sql.DB, args []string, out io.Writer) error {
	cmd, err := parseCommand(args, out)
	if err != nil {
		return err
	}
	return cmd(ctx, db)
}

// command is a parsed PAC-71 subcommand, ready to run against a database.
type command func(ctx context.Context, db *sql.DB) error

// parseCommand parses and checks one subcommand's flags without touching the
// database, so a usage error never applies a migration.
func parseCommand(args []string, out io.Writer) (command, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("%s", usage)
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	fs.SetOutput(out)
	operator := fs.String("operator", "", "who runs the step (audited)")
	reason := fs.String("reason", "", "why (audited)")
	runInfo := func() storage.OperatorRun { return storage.OperatorRun{Operator: *operator, Reason: *reason} }

	switch args[0] {
	case "free-address":
		account := fs.String("account", "", "the account id named in the server log")
		squatter := fs.Bool("squatter", false, "also remove every credential, connection and key (a squatter's account)")
		if err := fs.Parse(args[1:]); err != nil {
			return nil, err
		}
		id, err := uuid.Parse(*account)
		if err != nil {
			return nil, fmt.Errorf("-account: %w", err)
		}
		return func(ctx context.Context, db *sql.DB) error {
			placeholder, err := storage.FreeAddress(ctx, db, id, *squatter, runInfo())
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "account %s: address freed (now %s); sessions ended\n", id, placeholder)
			return nil
		}, nil
	case "reassign":
		account := fs.String("account", "", "the account id named in the server log")
		identity := fs.String("identity", "", "google:<sub> or microsoft:<tenant id>:<object id>, from the refusal log")
		evidence := fs.String("evidence", "", "authoritative or mailbox, from the refusal log")
		keep := fs.Bool("keep-calendar-credentials", false, "keep calendar credentials (only when confirmed issued to the new identity)")
		remove := fs.String("remove", "", "comma-separated other data the verified person disowns: "+strings.Join(storage.OtherRemovals(), ","))
		if err := fs.Parse(args[1:]); err != nil {
			return nil, err
		}
		id, err := uuid.Parse(*account)
		if err != nil {
			return nil, fmt.Errorf("-account: %w", err)
		}
		ident, err := storage.ParseOperatorIdentity(*identity)
		if err != nil {
			return nil, err
		}
		ev, ok := storage.ParseEvidence(*evidence)
		if !ok {
			return nil, fmt.Errorf("-evidence must be authoritative or mailbox")
		}
		var removals []string
		for _, r := range strings.Split(*remove, ",") {
			if r = strings.TrimSpace(r); r != "" {
				removals = append(removals, r)
			}
		}
		return func(ctx context.Context, db *sql.DB) error {
			res, err := storage.ReassignAccount(ctx, db, storage.ReassignRequest{
				Account: id, Identity: ident, Evidence: ev, KeepCalendarCredentials: *keep, Remove: removals, Run: runInfo(),
			})
			if err != nil {
				return err
			}
			prev := res.Previous
			if prev == "" {
				prev = "none"
			}
			fmt.Fprintf(out, "account %s: bound to %s (was %s); removed %v; generation %d\n", id, ident, prev, res.Removed, res.Gen)
			return nil
		}, nil
	case "advance-all":
		if err := fs.Parse(args[1:]); err != nil {
			return nil, err
		}
		return func(ctx context.Context, db *sql.DB) error {
			n, err := storage.AdvanceAllGenerations(ctx, db, runInfo())
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "advanced the session generation of %d accounts; every existing session now gets 401\n", n)
			return nil
		}, nil
	}
	return nil, fmt.Errorf("unknown command %q; %s", args[0], usage)
}
