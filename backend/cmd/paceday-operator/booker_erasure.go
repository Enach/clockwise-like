package main

// PAC-40 part B (contracts/features/PAC-40.md §5 "Operator listing", K4-5):
// `paceday-operator booker-erasure status [--check]`. Read-only: it opens
// the database without migrating, never applies a migration, needs no
// --operator/--reason and is not written to operator_audit. Its output holds
// ids and instants only: never a booker's name or email, never a link title.

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/storage"
)

const bookerErasureUsage = "usage: paceday-operator booker-erasure status [--check] (docs/runbook.md, \"Booker erasure (PAC-40)\")"

// bookerErasureApp is the command's application_name (PAC-84 contract 4.3).
const bookerErasureApp = "paceday-operator booker-erasure"

// bookerErasureSoon is how far ahead the listing looks: links due within it
// are listed with the due and overdue ones.
const bookerErasureSoon = 7 * 24 * time.Hour

// bookerErasureOverdue is the lateness past which a due link is "overdue": a
// running backend erases a link within 24 h of its due instant (spec §3.5).
const bookerErasureOverdue = 24 * time.Hour

// Exit codes (PAC-84 numbering, K4-5): exitOK listed, exitFailed failure
// (database unreachable, schema below 037 or dirty, query error), exitUsage
// usage, and exitOverdue with --check only, when a link is overdue.
const exitOverdue = 3

func runBookerErasure(args []string, getenv func(string) string, out, errw io.Writer, now func() time.Time) int {
	if len(args) == 0 || args[0] != "status" {
		fmt.Fprintln(errw, bookerErasureUsage)
		return exitUsage
	}
	fs := flag.NewFlagSet("booker-erasure status", flag.ContinueOnError)
	fs.SetOutput(errw)
	check := fs.Bool("check", false, "exit 3 when any deleted link's erasure is overdue (more than 24 h past due)")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() > 0 {
		if err == nil {
			fmt.Fprintf(errw, "booker-erasure status: unexpected argument %q\n", fs.Arg(0))
		}
		return exitUsage
	}
	dsn := getenv("DATABASE_URL")
	if dsn == "" {
		fmt.Fprintln(errw, "DATABASE_URL is required")
		return exitUsage
	}
	db, err := openDB(dsn, bookerErasureApp)
	if err != nil {
		fmt.Fprintln(errw, "booker-erasure:", err)
		return exitFailed
	}
	defer db.Close()
	ctx := context.Background()
	// PAC-116 (contract 4.5, challenge note of revision 3): a multi-table
	// read during a migration could be the other side of a deadlock, so the
	// listing refuses (exit 1) while a migrator holds the maintenance lock,
	// and keeps the connection it checked on open until it ends.
	pinned, code := holdPresence(ctx, db, "booker-erasure status", errw)
	if pinned == nil {
		return code
	}
	defer pinned.Close()

	want, err := storage.LifecycleMigrationVersion()
	if err != nil {
		fmt.Fprintln(errw, "booker-erasure:", err)
		return exitFailed
	}
	version, dirty, present, err := storage.SchemaVersion(ctx, db)
	if err != nil {
		fmt.Fprintln(errw, "booker-erasure:", err)
		return exitFailed
	}
	if !present || dirty || version < want {
		state := fmt.Sprintf("at version %d", version)
		switch {
		case !present:
			state = "not initialised"
		case dirty:
			state = fmt.Sprintf("dirty at version %d", version)
		}
		fmt.Fprintf(errw, "booker-erasure status: the database schema is %s, which is not at or above migration %d (PAC-40); cannot list, nothing changed and no migration applied. Start this release's backend to upgrade (docs/runbook.md, \"Booker erasure (PAC-40)\")\n", state, want)
		return exitFailed
	}

	at := now().UTC()
	rows, err := storage.ListBookerErasureStatus(ctx, db, at, bookerErasureSoon)
	if err != nil {
		// The class only: a query error never reaches the terminal with row data.
		fmt.Fprintln(errw, "booker-erasure status: query failed:", engine.ErasureErrorClass(err))
		return exitFailed
	}
	overdue := 0
	fmt.Fprintf(out, "booker-erasure status at=%s: %d deleted link(s) due, overdue or due within 7 days\n", at.Format(time.RFC3339), len(rows))
	for _, r := range rows {
		mark := ""
		if at.Sub(r.ErasureDueAt) > bookerErasureOverdue {
			mark = " overdue"
			overdue++
		}
		fmt.Fprintf(out, "link=%s owner=%s deleted_at=%s last_booking_end_at=%s erasure_due_at=%s awaiting=%d%s\n",
			r.LinkID, r.OwnerUserID, r.DeletedAt.UTC().Format(time.RFC3339),
			r.LastBookingEndAt.UTC().Format(time.RFC3339), r.ErasureDueAt.UTC().Format(time.RFC3339), r.Awaiting, mark)
	}
	fmt.Fprintln(out, "last run: the newest \"booker-erasure: run\" line in the backend log")
	if *check && overdue > 0 {
		fmt.Fprintf(errw, "booker-erasure status: %d link(s) overdue\n", overdue)
		return exitOverdue
	}
	return exitOK
}
