package main

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/config"
	"github.com/Enach/paceday/backend/internal/secretbox"
	"github.com/Enach/paceday/backend/internal/secretbox/secretboxtest"
	"github.com/Enach/paceday/backend/internal/testdb"
	"github.com/Enach/paceday/backend/storage"
	"github.com/getsentry/sentry-go"
	"github.com/google/uuid"
)

// PAC-84 contract 5.3: the backend's key load, refusal texts, automatic
// conversion and refresher wiring; AC-19: neither the log nor the error
// reporter ever carries a secret, the key or ciphertext.

func pac84Keys(t *testing.T, id string) (secretbox.KeyConfig, []byte) {
	t.Helper()
	raw := secretboxtest.RandomKey()
	cfg, err := config.DecideEncryptionKeys(func(k string) string {
		if k == "PACEDAY_ENCRYPTION_KEY" {
			return id + ":" + base64.StdEncoding.EncodeToString(raw)
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	return cfg, raw
}

func TestPAC84_BackendKeyLoadConvertAndReport(t *testing.T) {
	logs := captureLog(t)
	tr := &stubTransport{}
	if err := sentry.Init(sentry.ClientOptions{Dsn: "https://k@sentry.invalid/1", Transport: tr}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sentry.Init(sentry.ClientOptions{}) })
	dsn, cleanup, err := testdb.Create()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cleanup() })
	db, err := storage.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	prev := secretbox.Current()
	t.Cleanup(func() { secretbox.Install(prev); secretbox.SetSealGate(nil); secretbox.SetReporter(nil) })

	const canaryA, canaryB = "canary-webcal-6f1c2b9a8e7d4c3b2a19", "sk-canary-key-0a9b8c7d6e5f4a3b2c1d"
	u := uuid.New()
	if _, err := db.Exec(`INSERT INTO users (id, email, name) VALUES ($1, $2, 'B')`, u, u.String()+"@b.example"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO settings (user_id, webcal_url, llm_api_key_openai) VALUES ($1, $2, $3)`, u, canaryA, canaryB); err != nil {
		t.Fatal(err)
	}

	cfg, raw := pac84Keys(t, "boot-1")
	ctx := context.Background()
	enc, err := startEncryption(ctx, db, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), `encryption: key "boot-1" active; 1 data keys (active 1); 0 re-wrapped from previous keys`) {
		t.Fatalf("start line missing: %s", logs.String())
	}
	enc.tick, enc.convPause = 20*time.Millisecond, time.Millisecond
	stop := enc.startBackground()
	deadline := time.Now().Add(10 * time.Second)
	for {
		st, err := storage.ReadEncryptionStatus(ctx, db)
		if err == nil && st.PlainTotal() == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("automatic conversion did not finish: %s", logs.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond) // a refresher tick or two
	stop()
	if !strings.Contains(logs.String(), "encryption: conversion done: 2 values encrypted") {
		t.Fatalf("conversion lines: %s", logs.String())
	}
	// AC-26: the KEK unwrapped exactly the one data key, though requests and
	// refresher ticks ran.
	if _, err := storage.GetOrCreateSettingsByUser(db, u); err != nil {
		t.Fatal(err)
	}
	if n := secretbox.Current().Unwraps(); n != 1 {
		t.Fatalf("unwraps %d, want 1", n)
	}

	// AC-16 log + reporter line, AC-19: a failing secret is reported by id.
	if _, err := db.Exec(`UPDATE settings SET llm_api_key_anthropic = llm_api_key_openai WHERE user_id = $1`, u); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.GetOrCreateSettingsByUser(db, u); err != nil {
		t.Fatal(err)
	}
	sentry.Flush(time.Second)
	var reported []string
	tr.mu.Lock()
	for _, e := range tr.events {
		b, _ := json.Marshal(e)
		reported = append(reported, string(b))
	}
	tr.mu.Unlock()
	all := logs.String() + strings.Join(reported, "\n")
	if !strings.Contains(strings.Join(reported, "\n"), "encryption: cannot decrypt settings.llm_api_key_anthropic") {
		t.Fatalf("reporter did not receive the line: %v", reported)
	}
	var stored string
	_ = db.QueryRow(`SELECT webcal_url FROM settings WHERE user_id = $1`, u).Scan(&stored)
	for _, bad := range []string{canaryA, canaryB, stored, base64.StdEncoding.EncodeToString(raw), hex.EncodeToString(raw), string(raw), "pdenc:1:"} {
		if strings.Contains(all, bad) {
			t.Fatalf("log or reporter carries %q", bad)
		}
	}

	enc.removeHeartbeat()
	if hb, _ := storage.LiveHeartbeats(ctx, db); len(hb) != 0 {
		t.Fatal("heartbeat not removed at shutdown")
	}

	// AC-9: a wrong key refuses with contract 9.1's text, no key material.
	wrong, wraw := pac84Keys(t, "boot-1")
	_, err = startEncryption(ctx, db, wrong)
	if err == nil || err.Error() != `refusing to start: key "boot-1" (PACEDAY_ENCRYPTION_KEY) does not open data key 1 (wrapped by key "boot-1") (docs/runbook.md, "Encryption key")` {
		t.Fatalf("wrong key: %v", err)
	}
	if strings.Contains(err.Error(), base64.StdEncoding.EncodeToString(wraw)) {
		t.Fatal("refusal echoes the key")
	}
	other, _ := pac84Keys(t, "other")
	if _, err := startEncryption(ctx, db, other); err == nil || !strings.HasPrefix(err.Error(), `refusing to start: data key 1 is wrapped by key "boot-1", which is neither PACEDAY_ENCRYPTION_KEY ("other")`) {
		t.Fatalf("unknown key: %v", err)
	}
}
