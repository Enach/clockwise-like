package storage

import (
	"context"
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
	db, _, _ := openEncDB(t)
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
	r := &Refresher{DB: db, Cfg: cfg, Inst: inst, Lease: NewLease(time.Now()),
		Logf: func(f string, a ...any) { logged = append(logged, f) }}
	// A cancelled context: the data-key read and the heartbeat both fail.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
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
