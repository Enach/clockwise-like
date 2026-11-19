package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/storage"
)

// Review of #326, item 1: Ctrl-C/SIGTERM cancel the run's context (main
// uses signal.NotifyContext); the pool sends PostgreSQL a cancel request, so
// a run waiting on a lock ends server-side at once and its locks go with it,
// not at the run's deadline. Here the run waits on a held booking with an
// 8 s bound; it is cancelled about 1 s into the wait; a third session must
// get the team row the run had locked well before the deadline.
func TestPAC134_InterruptReleasesLocksAtOnce(t *testing.T) {
	p := newP134(t)
	f := p.fixture()
	holder, err := p.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback() }()
	if _, err := holder.Exec(`SELECT 1 FROM bookings WHERE id = $1 FOR UPDATE`, f.bookingL1); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		code      int
		out, errw string
	}
	done := make(chan result, 1)
	go func() {
		var out, errw strings.Builder
		code := runEraseAddress(ctx, append([]string{"-max-duration", "8s"}, pac134Args...),
			func(k string) string { return map[string]string{"DATABASE_URL": p.dsn}[k] }, strings.NewReader(pac134X+"\n"), &out, &errw)
		done <- result{code, out.String(), errw.String()}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for p.count(`SELECT count(*) FROM pg_stat_activity WHERE application_name = $1 AND wait_event_type = 'Lock'`, storage.EraseAddressApp) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the run never waited on the held booking")
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(time.Second)
	cancel()
	cancelled := time.Now()
	r := <-done
	if r.code != exitFailed || r.errw != "erase-address: failed: interrupted; no data changed\n" {
		t.Fatalf("exit %d\n%s\n%s", r.code, r.out, r.errw)
	}
	// The team row the run locked (lock_teams) is free well before the
	// run's 8 s deadline.
	for {
		probe, err := p.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		_, err = probe.Exec(`SELECT 1 FROM teams WHERE id = $1 FOR UPDATE NOWAIT`, f.ta)
		_ = probe.Rollback()
		if err == nil {
			break
		}
		if time.Since(cancelled) > 1500*time.Millisecond {
			t.Fatalf("the run's lock on the team was still held %s after the interrupt", time.Since(cancelled))
		}
		time.Sleep(20 * time.Millisecond)
	}
	if p.count(`SELECT count(*) FROM operator_audit WHERE action = 'erase_address'`) != 0 ||
		p.count(`SELECT count(*) FROM team_added_members WHERE email = $1`, pac134X) != 1 {
		t.Fatal("the interrupted run changed data")
	}
}

// The same through a process that exits as soon as the command returns, as
// paceday-operator does after Ctrl-C (main calls os.Exit at once). pgx's
// default context handler sends its cancel request from a goroutine after
// closing the connection, which a process that exits never runs; the
// backend then keeps waiting, holding the run's locks, until its own
// timeout. With the cancel-request handler the server has the cancel before
// the command returns. The child is this test binary
// (TestPAC134_InterruptChild), interrupted 1 s into its run.
func TestPAC134_InterruptedProcessLeavesNoLocks(t *testing.T) {
	p := newP134(t)
	f := p.fixture()
	holder, err := p.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback() }()
	if _, err := holder.Exec(`SELECT 1 FROM bookings WHERE id = $1 FOR UPDATE`, f.bookingL1); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestPAC134_InterruptChild$")
	cmd.Env = append(os.Environ(), "PAC134_INTERRUPT_CHILD_DSN="+p.dsn)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err = cmd.Run()
	exited := time.Now()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != exitFailed || !strings.Contains(stderr.String(), "erase-address: failed: interrupted; no data changed") {
		t.Fatalf("child: %v\n%s", err, stderr.String())
	}
	for {
		probe, err := p.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		_, err = probe.Exec(`SELECT 1 FROM teams WHERE id = $1 FOR UPDATE NOWAIT`, f.ta)
		_ = probe.Rollback()
		if err == nil {
			break
		}
		if time.Since(exited) > 500*time.Millisecond {
			t.Fatalf("the exited run's lock on the team was still held %s after the process ended", time.Since(exited))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestPAC134_InterruptChild is the child process of
// TestPAC134_InterruptedProcessLeavesNoLocks; it does nothing unless that
// test started it.
func TestPAC134_InterruptChild(t *testing.T) {
	dsn := os.Getenv("PAC134_INTERRUPT_CHILD_DSN")
	if dsn == "" {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(time.Second, cancel)
	code := runEraseAddress(ctx, append([]string{"-max-duration", "8s"}, pac134Args...),
		func(k string) string { return map[string]string{"DATABASE_URL": dsn}[k] }, strings.NewReader(pac134X+"\n"), os.Stdout, os.Stderr)
	os.Exit(code)
}
