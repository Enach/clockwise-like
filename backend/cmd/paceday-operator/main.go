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
//
// DATABASE_URL names the database. Migrations are applied first, as the server
// does on start.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL is required")
		os.Exit(2)
	}
	db, err := storage.Open(dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer db.Close()
	if err := run(context.Background(), db, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

const usage = "usage: paceday-operator free-address|reassign|advance-all [flags] (see -h on each)"

// run executes one operator command against db and reports on out.
func run(ctx context.Context, db *sql.DB, args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("%s", usage)
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
			return err
		}
		id, err := uuid.Parse(*account)
		if err != nil {
			return fmt.Errorf("-account: %w", err)
		}
		placeholder, err := storage.FreeAddress(ctx, db, id, *squatter, runInfo())
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "account %s: address freed (now %s); sessions ended\n", id, placeholder)
		return nil
	case "reassign":
		account := fs.String("account", "", "the account id named in the server log")
		identity := fs.String("identity", "", "google:<sub> or microsoft:<tenant id>:<object id>, from the refusal log")
		evidence := fs.String("evidence", "", "authoritative or mailbox, from the refusal log")
		keep := fs.Bool("keep-calendar-credentials", false, "keep calendar credentials (only when confirmed issued to the new identity)")
		remove := fs.String("remove", "", "comma-separated other data the verified person disowns: "+strings.Join(storage.OtherRemovals(), ","))
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		id, err := uuid.Parse(*account)
		if err != nil {
			return fmt.Errorf("-account: %w", err)
		}
		ident, err := storage.ParseOperatorIdentity(*identity)
		if err != nil {
			return err
		}
		ev, ok := storage.ParseEvidence(*evidence)
		if !ok {
			return fmt.Errorf("-evidence must be authoritative or mailbox")
		}
		var removals []string
		for _, r := range strings.Split(*remove, ",") {
			if r = strings.TrimSpace(r); r != "" {
				removals = append(removals, r)
			}
		}
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
	case "advance-all":
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		n, err := storage.AdvanceAllGenerations(ctx, db, runInfo())
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "advanced the session generation of %d accounts; every existing session now gets 401\n", n)
		return nil
	}
	return fmt.Errorf("unknown command %q; %s", args[0], usage)
}
