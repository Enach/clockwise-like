package main

// PAC-40 part B (contract §5 "Operator listing", K4-5; AC-37):
// `paceday-operator booker-erasure status [--check]`.

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

var erasureNow = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

func (e *opEnv) erasure(env map[string]string, args ...string) (int, string, string) {
	e.t.Helper()
	all := map[string]string{"DATABASE_URL": e.dsn}
	for k, v := range env {
		all[k] = v
	}
	var out, errw bytes.Buffer
	code := runBookerErasure(args, func(k string) string { return all[k] }, &out, &errw, func() time.Time { return erasureNow })
	return code, out.String(), errw.String()
}

// deletedLink stores a deleted link whose single booking ended at end, with a
// booker name, an email and a title the output must never show.
func (e *opEnv) deletedLink(owner uuid.UUID, slug string, end time.Time) uuid.UUID {
	e.t.Helper()
	l, err := storage.CreateSchedulingLink(e.db, &storage.SchedulingLink{
		OwnerUserID: owner, Slug: slug, Title: "Secret Title " + slug, DurationOptions: []int{30},
		DaysOfWeek: []int{1}, WindowStart: "09:00", WindowEnd: "17:00", UsageType: "reusable", Active: true,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.db.Exec(`INSERT INTO bookings (link_id, booker_name, booker_email, start_time, end_time, notes)
		VALUES ($1, $2, $3, $4, $5, '')`, l.ID, "Booker Name "+slug, slug+"@booker.test", end.Add(-30*time.Minute), end); err != nil {
		e.t.Fatal(err)
	}
	if ok, err := storage.DeleteSchedulingLink(e.db, l.ID); err != nil || !ok {
		e.t.Fatal(ok, err)
	}
	return l.ID
}

func TestPAC40B_StatusUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"erase"}, {"status", "extra"}, {"status", "--operator", "x"}} {
		var out, errw bytes.Buffer
		code := runBookerErasure(args, func(k string) string { return "postgres://unused" }, &out, &errw, time.Now)
		if code != exitUsage || out.Len() != 0 {
			t.Errorf("%v: exit %d out %q; want 2 and nothing listed", args, code, out.String())
		}
	}
	var out, errw bytes.Buffer
	if code := runBookerErasure([]string{"status"}, func(string) string { return "" }, &out, &errw, time.Now); code != exitUsage ||
		!strings.Contains(errw.String(), "DATABASE_URL is required") {
		t.Errorf("no DATABASE_URL: exit %d, %q", code, errw.String())
	}
}

// AC-37: the listing (overdue, due within 7 days; not later, not erased),
// ids and instants only, exit 0; --check exits 3 only when a link is overdue.
func TestPAC40B_StatusListsAndChecks(t *testing.T) {
	e := newOpEnv(t)
	owner, err := storage.UpsertUser(e.db, "pac40b-op@example.com", "Owner", "", "google", "pac40b-op")
	if err != nil {
		t.Fatal(err)
	}
	var apps []string
	prev := openDB
	openDB = func(dsn, app string) (*sql.DB, error) { apps = append(apps, app); return prev(dsn, app) }
	t.Cleanup(func() { openDB = prev })

	code, out, _ := e.erasure(nil, "status", "--check")
	if code != exitOK || !strings.Contains(out, "0 deleted link(s)") {
		t.Fatalf("empty: exit %d, %q", code, out)
	}

	soon := e.deletedLink(owner.ID, "op-soon", erasureNow.Add(-storage.BookerRetention+3*24*time.Hour))
	code, out, _ = e.erasure(nil, "status", "-check")
	if code != exitOK || !strings.Contains(out, "link="+soon.String()) || strings.Contains(out, "overdue\n") {
		t.Fatalf("due soon only, --check: exit %d, %q; want 0 and not overdue", code, out)
	}

	// Due 12 h ago: due but not yet overdue; due 2 days ago: overdue.
	due := e.deletedLink(owner.ID, "op-due", erasureNow.Add(-storage.BookerRetention-12*time.Hour))
	late := e.deletedLink(owner.ID, "op-late", erasureNow.Add(-storage.BookerRetention-48*time.Hour))
	e.deletedLink(owner.ID, "op-later", erasureNow.Add(-storage.BookerRetention+8*24*time.Hour))
	code, out, _ = e.erasure(nil, "status")
	if code != exitOK {
		t.Fatalf("listing exit %d", code)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 5 || !strings.Contains(lines[0], "3 deleted link(s)") {
		t.Fatalf("listing = %q; want header, three links, the last-run note", out)
	}
	lateEnd := erasureNow.Add(-storage.BookerRetention - 48*time.Hour)
	wantLate := "link=" + late.String() + " owner=" + owner.ID.String() + " deleted_at="
	if !strings.HasPrefix(lines[1], wantLate) ||
		!strings.HasSuffix(lines[1], " last_booking_end_at="+lateEnd.Format(time.RFC3339)+
			" erasure_due_at="+lateEnd.Add(storage.BookerRetention).Format(time.RFC3339)+" awaiting=1 overdue") {
		t.Errorf("overdue line = %q", lines[1])
	}
	if !strings.HasPrefix(lines[2], "link="+due.String()) || strings.HasSuffix(lines[2], "overdue") {
		t.Errorf("due line = %q; want not overdue (12 h past due)", lines[2])
	}
	if !strings.HasPrefix(lines[3], "link="+soon.String()) {
		t.Errorf("soon line = %q", lines[3])
	}
	for _, s := range []string{"Booker Name", "@booker.test", "Secret Title", "op-late", "op-later"} {
		if strings.Contains(out, s) {
			t.Errorf("output holds %q: %q", s, out)
		}
	}
	if code, _, errw := e.erasure(nil, "status", "--check"); code != exitOverdue || !strings.Contains(errw, "1 link(s) overdue") {
		t.Errorf("--check with an overdue link: exit %d, %q; want 3", code, errw)
	}
	for _, a := range apps {
		if a != "paceday-operator booker-erasure" {
			t.Errorf("application_name %q", a)
		}
	}
	var audited int
	if err := e.db.QueryRow(`SELECT count(*) FROM operator_audit`).Scan(&audited); err != nil || audited != 0 {
		t.Errorf("operator_audit rows = %d, %v; the listing is not audited", audited, err)
	}
}

// K4-5: below 037 (or dirty, or unreachable) the command fails with exit 1,
// even under --check, and never applies a migration.
func TestPAC40B_StatusNeverMigrates(t *testing.T) {
	want, err := storage.LifecycleMigrationVersion()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(storage.SetMigrationCeilingForTest(want - 1))
	e := newOpEnv(t)
	version := func() (uint, bool) {
		v, dirty, _, err := storage.SchemaVersion(context.Background(), e.db)
		if err != nil {
			t.Fatal(err)
		}
		return v, dirty
	}
	if v, _ := version(); v != want-1 {
		t.Fatalf("fixture at %d; want %d", v, want-1)
	}
	for _, args := range [][]string{{"status"}, {"status", "--check"}} {
		code, out, errw := e.erasure(nil, args...)
		if code != exitFailed || out != "" || !strings.Contains(errw, "no migration applied") {
			t.Errorf("%v below 037: exit %d out %q err %q; want 1", args, code, out, errw)
		}
	}
	if v, _ := version(); v != want-1 {
		t.Errorf("schema moved to %d: the command applied a migration", v)
	}

	if _, err := e.db.Exec(`UPDATE schema_migrations SET dirty = true`); err != nil {
		t.Fatal(err)
	}
	if code, _, errw := e.erasure(nil, "status"); code != exitFailed || !strings.Contains(errw, "dirty") {
		t.Errorf("dirty: exit %d %q", code, errw)
	}
	if _, err := e.db.Exec(`DROP TABLE schema_migrations`); err != nil {
		t.Fatal(err)
	}
	if code, _, errw := e.erasure(nil, "status"); code != exitFailed || !strings.Contains(errw, "not initialised") {
		t.Errorf("no schema: exit %d %q", code, errw)
	}
	if code, _, _ := e.erasure(map[string]string{"DATABASE_URL": "postgres://test:test@127.0.0.1:1/none?sslmode=disable&connect_timeout=2"}, "status"); code != exitFailed {
		t.Errorf("unreachable database: exit %d; want 1", code)
	}
}

// AC-37 (unit half): the runbook section the command points to exists and
// covers every item of spec §3.5 "Runbook" and contract §5/§7 (K4-8).
func TestPAC40B_RunbookSection(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "runbook.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := strings.ReplaceAll(string(raw), "\r\n", "\n")
	start := strings.Index(doc, "\n## Booker erasure (PAC-40)\n")
	if start < 0 {
		t.Fatal(`docs/runbook.md has no "## Booker erasure (PAC-40)" section`)
	}
	section := doc[start+1:]
	if end := strings.Index(section[3:], "\n## "); end >= 0 {
		section = section[:end+3]
	}
	for what, phrase := range map[string]string{
		"reach: calendar events":         "calendar event",
		"reach: sent emails":             "confirmation email",
		"reach: backups, 14 days":        "14 days",
		"retention bounds survival":      "bounds how long an\nerased value survives",
		"start-up and daily":             "once at every start-up, then every 24 hours",
		"the run line":                   "booker-erasure: run at=",
		"the status command":             "/paceday-operator booker-erasure status [--check]",
		"exit 3 for monitoring":          "3 with `--check` only when at least one link is overdue",
		"after a restore":                "check the next `booker-erasure: run` line before anyone\nuses the restored data",
		"failures>0":                     "`failures>0`",
		"irreversible, even by a revert": "not by a code revert either",
		"K4-8: a rollback of A":          "becomes an ordinary **paused** link",
		"K4-8: record deleted ids":       "record the deleted link ids",
	} {
		if !strings.Contains(strings.Join(strings.Fields(section), " "), strings.Join(strings.Fields(phrase), " ")) {
			t.Errorf("runbook section lacks %s (%q)", what, phrase)
		}
	}
}
