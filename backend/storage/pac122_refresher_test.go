package storage

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/internal/lifecycle/lifecycletest"
	"github.com/Enach/paceday/backend/internal/secretbox"
	"github.com/google/uuid"
)

// PAC-122 SF-099: the keyring refresher's failed steps are reported through
// the lifecycle hook, not only logged; the tick carries on as before.
func TestPAC122_RefresherFailuresReported(t *testing.T) {
	db, dsn, _ := openEncDB(t)
	cfg := encKeys(t, "pac122")
	inst := Instance{ID: uuid.New()}
	load, err := LoadKeyring(context.Background(), db, cfg, inst)
	if err != nil {
		t.Fatal(err)
	}
	prev := secretbox.Current()
	secretbox.Install(load.Keyring)
	defer secretbox.Install(prev)

	rec := lifecycletest.Record(t)
	var logged []string
	// A closed pool: the data-key read and the heartbeat both fail.
	closed, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	_ = closed.Close()
	r := &Refresher{DB: closed, Cfg: cfg, Inst: inst, Lease: NewLease(time.Now()),
		Logf: func(f string, a ...any) { logged = append(logged, f) }}
	ctx := context.Background()
	if !r.Tick(ctx) {
		t.Fatal("a failed tick stopped the refresher (behaviour changed)")
	}
	for _, step := range []string{"refresher: read data keys", "heartbeat"} {
		got := rec.Matching("encryption", step)
		if len(got) != 1 || got[0].Unit.ID != "keyring-refresher" {
			t.Errorf("%s: reports %+v", step, rec.All())
		}
	}
	if len(logged) != 2 {
		t.Errorf("logged %v", logged)
	}
	for _, r := range rec.All() {
		if strings.Contains(r.Err.Error(), "pdenc:") {
			t.Fatalf("report carries ciphertext: %v", r.Err)
		}
	}
}

// PAC-122 review: a refresher step cut short by shutdown (its context
// cancelled) is logged, not reported as a failure.
func TestPAC122_RefresherShutdownNotReported(t *testing.T) {
	db, _, _ := openEncDB(t)
	cfg := encKeys(t, "pac122-stop")
	inst := Instance{ID: uuid.New()}
	load, err := LoadKeyring(context.Background(), db, cfg, inst)
	if err != nil {
		t.Fatal(err)
	}
	prev := secretbox.Current()
	secretbox.Install(load.Keyring)
	defer secretbox.Install(prev)
	rec := lifecycletest.Record(t)
	var logged int
	r := &Refresher{DB: db, Cfg: cfg, Inst: inst, Lease: NewLease(time.Now()),
		Logf: func(string, ...any) { logged++ }}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r.Tick(ctx)
	if logged == 0 {
		t.Fatal("the cancelled steps were not logged; the test is not observing them")
	}
	if all := rec.All(); len(all) != 0 {
		t.Fatalf("shutdown reported as failures: %+v", all)
	}
}
