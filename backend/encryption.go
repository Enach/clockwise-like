package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"os"
	"sync"
	"time"

	"github.com/Enach/paceday/backend/config"
	"github.com/Enach/paceday/backend/internal/lifecycle"
	"github.com/Enach/paceday/backend/internal/secretbox"
	"github.com/Enach/paceday/backend/storage"
	"github.com/getsentry/sentry-go"
	"github.com/google/uuid"
)

// PAC-84 contract 5.3: the backend's key load (step 4) and, after the
// listener opens, the keyring refresher and the automatic conversion
// (step 7).

type encryptionRuntime struct {
	db        *sql.DB
	cfg       secretbox.KeyConfig
	inst      storage.Instance
	lease     *storage.Lease
	refresher *storage.Refresher
	fatal     chan string
	// Intervals, overridable by tests.
	tick, convRetry, convPause time.Duration
}

// startEncryption loads the keyring (heartbeat, offline-operation check,
// first data key, re-wrap, unwrap), installs it with the seal lease and the
// error reporter, and logs contract 9.4's start lines. A failure is
// contract 9.1's refusal text.
func startEncryption(ctx context.Context, db *sql.DB, cfg secretbox.KeyConfig) (*encryptionRuntime, error) {
	host, _ := os.Hostname()
	r := &encryptionRuntime{
		db: db, cfg: cfg,
		inst:  storage.Instance{ID: uuid.New(), Hostname: host},
		fatal: make(chan string, 1),
		tick:  storage.HeartbeatInterval, convRetry: 60 * time.Second, convPause: 50 * time.Millisecond,
	}
	sent := time.Now()
	load, err := storage.LoadKeyring(ctx, db, cfg, r.inst)
	if err != nil {
		return nil, errors.New(refusalText(err))
	}
	r.lease = storage.NewLease(sent)
	r.lease.Logf = log.Printf
	secretbox.Install(load.Keyring)
	secretbox.SetSealGate(r.lease.Check)
	secretbox.SetReporter(func(line string) { sentry.CaptureMessage(line) })
	log.Printf("encryption: key %q active; %d data keys (active %d); %d re-wrapped from previous keys",
		cfg.Active.ID, load.DataKeys, load.Active, load.Rewrapped)
	for _, id := range load.UnusedPrevious {
		log.Printf("encryption: previous key %q wraps no data key; remove it once the backup retention (14 days) has passed %s", id, secretbox.RunbookRef)
	}
	r.refresher = &storage.Refresher{
		DB: db, Cfg: cfg, Inst: r.inst, Lease: r.lease, Logf: log.Printf,
		Shutdown: func(reason string) {
			select {
			case r.fatal <- reason:
			default:
			}
		},
	}
	return r, nil
}

// refusalText is contract 9.1's line for a key-load failure.
func refusalText(err error) string {
	switch {
	case errors.Is(err, secretbox.ErrWrongKey), errors.Is(err, secretbox.ErrUnknownKEK),
		errors.Is(err, secretbox.ErrNoActiveDEK), errors.Is(err, storage.ErrOfflineOperation):
		msg := err.Error()
		for _, s := range []error{secretbox.ErrWrongKey, secretbox.ErrUnknownKEK, secretbox.ErrNoActiveDEK, storage.ErrOfflineOperation} {
			if p := s.Error() + ": "; len(msg) > len(p) && msg[:len(p)] == p {
				msg = msg[len(p):]
			}
		}
		return "refusing to start: " + msg + " " + secretbox.RunbookRef
	}
	return config.EncryptionRefusal("refusing to start: encryption key load failed: ", err)
}

// startBackground starts the refresher and the automatic conversion and
// returns the function that stops them (called when scheduling stops).
func (r *encryptionRuntime) startBackground() (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(r.tick)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			g := lifecycle.Default()
			if g.ShuttingDown() {
				return
			}
			done := g.Track(lifecycle.Unit{Kind: "encryption", ID: "keyring-refresher"})
			ok := r.refresher.Tick(ctx)
			done()
			if !ok {
				return
			}
		}
	}()
	lifecycle.Go(lifecycle.Unit{Kind: "encryption", ID: "conversion"}, func(work context.Context) {
		r.convert(ctx)
	})
	var once sync.Once
	return func() { once.Do(cancel) }
}

// convert is the backend's automatic conversion (contract 8.3): once per
// process start, under the maintenance lock, retried every convRetry while
// another process holds it. Not audited; logged (9.4).
func (r *encryptionRuntime) convert(ctx context.Context) {
	for {
		lock, err := storage.TryMaintenanceLock(ctx, r.db)
		if err == nil {
			defer lock.Release()
			break
		}
		var held *storage.LockHeldError
		if errors.As(err, &held) && held.Holder != nil {
			log.Printf("encryption: conversion waiting: maintenance lock held by %s (pid %d)", held.Holder.AppName, held.Holder.PID)
		} else {
			log.Printf("encryption: conversion waiting: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(r.convRetry):
		}
	}
	log.Printf("encryption: conversion started")
	start := time.Now()
	res, err := storage.RunPass(ctx, r.db, storage.PassOptions{
		Kind: storage.PassConvert, Batch: 100, Pause: r.convPause,
		Keyring: secretbox.Current(), Seal: secretbox.Seal,
	})
	n := int64(0)
	if res != nil {
		n = res.Total("encrypted")
	}
	if err != nil {
		log.Printf("encryption: conversion stopped after %d values: %v", n, err)
		return
	}
	log.Printf("encryption: conversion done: %d values encrypted in %s", n, time.Since(start).Truncate(time.Millisecond))
	if res.Partial {
		log.Printf("encryption: conversion left %d values to the next run", res.Remaining)
	}
}

// removeHeartbeat deletes this process's heartbeat at graceful shutdown.
func (r *encryptionRuntime) removeHeartbeat() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := storage.DeleteHeartbeat(ctx, r.db, r.inst); err != nil {
		log.Printf("encryption: remove heartbeat: %v", err)
	}
}
