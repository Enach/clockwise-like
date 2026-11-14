package auth

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/internal/oauthfake"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// PAC-80 items 3 and 4 (spec docs/specs/PAC-80.md AC-10..AC-19): a renewal
// for a stale (or unreadable) session never reaches the provider, and once
// the provider has answered, the loss decision and the renewal's save finish
// even if the requester hangs up, under the session re-check and a bound.

// sourceFor builds provider's renewing source over user's stored credential
// for ctx, on db (which may wrap the test database).
func sourceFor(t *testing.T, ctx context.Context, db storage.DBTX, raw *sql.DB, d *oauthfake.Server, provider string, user uuid.UUID) oauth2.TokenSource {
	t.Helper()
	switch provider {
	case storage.CredentialGoogle:
		tok, err := LoadUserToken(raw, user)
		if err != nil || tok == nil {
			t.Fatalf("load google: %v %v", tok, err)
		}
		return UserTokenSource(ctx, db, googleCfg(d), user, tok)
	case storage.CredentialOutlook:
		tok, err := LoadMicrosoftToken(raw, user)
		if err != nil || tok == nil {
			t.Fatalf("load outlook: %v %v", tok, err)
		}
		return MicrosoftTokenSource(ctx, db, msCfg(d), user, tok)
	default:
		cred, err := storage.LoadZoomCredential(raw, user)
		if err != nil || cred == nil {
			t.Fatalf("load zoom: %v %v", cred, err)
		}
		return ZoomTokenSource(ctx, db, zoomCfg(d), user, cred)
	}
}

// seedPAC80 stores an expired credential (Zoom: no stored expiry, AC-11).
func seedPAC80(t *testing.T, db *sql.DB, provider string, user uuid.UUID, access, refresh string) {
	t.Helper()
	if provider == storage.CredentialZoom {
		storeZoom(t, db, user, access, refresh, nil)
		return
	}
	seed(t, db, provider, user, access, refresh, past)
}

func assertNothingRecorded(t *testing.T, db *sql.DB, provider string, user uuid.UUID) {
	t.Helper()
	if lost, _ := storage.IsCredentialLost(db, user, provider); lost {
		t.Error("a loss was recorded")
	}
	if n := len(auditOf(t, db, user)); n != 0 {
		t.Errorf("activity entries = %d, want 0", n)
	}
}

// refusingBeginDB is the test database with every transaction refused, so
// the in-request re-check cannot read the generation (AC-12).
type refusingBeginDB struct{ *sql.DB }

func (refusingBeginDB) BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error) {
	return nil, errors.New("pac80: database refuses the re-check")
}

func (refusingBeginDB) Begin() (*sql.Tx, error) {
	return nil, errors.New("pac80: database refuses the re-check")
}

// AC-10, AC-11, AC-12: no provider call, the stale (or unavailable) error,
// no loss, no entry, the stored credential unchanged.
func TestPAC80_RenewalForAStaleSessionNeverReachesTheProvider(t *testing.T) {
	for _, p := range providers {
		for _, tc := range []struct {
			name string
			want error
		}{{"stale", storage.ErrStaleSession}, {"unavailable", storage.ErrGenerationUnavailable}} {
			t.Run(p+"/"+tc.name, func(t *testing.T) {
				db := openTestDB(t)
				d := oauthfake.New()
				defer d.Close()
				d.Rotate(false)
				u := seedUser(t, db, "pac80-stale-"+p+"@example.com")
				seedPAC80(t, db, p, u, "a-old", "a-refresh")
				before := stored(t, db, p, u)
				var handle storage.DBTX = db
				g := &storage.SessionGuard{UserID: u, Gen: 0}
				if tc.want == storage.ErrStaleSession {
					if _, err := db.Exec(`UPDATE users SET session_generation = session_generation + 1 WHERE id = $1`, u); err != nil {
						t.Fatal(err)
					}
				} else {
					handle = refusingBeginDB{db}
				}
				ctx := storage.WithSessionGuard(context.Background(), g)
				tok, err := sourceFor(t, ctx, handle, db, d, p, u).Token()
				if !errors.Is(err, tc.want) || tok != nil {
					t.Fatalf("Token() = %v, %v; want %v", tok, err, tc.want)
				}
				if errors.Is(err, ErrConnectionLost) {
					t.Fatal("a refused renewal was reported as a lost connection")
				}
				if n := len(d.Calls("/token")); n != 0 {
					t.Fatalf("the provider's token endpoint received %d requests, want 0", n)
				}
				assertNothingRecorded(t, db, p, u)
				if after := stored(t, db, p, u); after == nil || after.AccessToken != before.AccessToken || after.RefreshToken != before.RefreshToken || !after.Expiry.Equal(before.Expiry) {
					t.Fatalf("stored credential changed: %+v -> %+v", before, after)
				}
			})
		}
	}
}

// hangUp is an HTTP transport that lets the provider's answer arrive and
// then has the requester hang up: before returning the response it reads
// the body in full, runs before (if any) and cancels the caller's context.
// With early set, the requester hangs up BEFORE the request is sent (AC-19).
type hangUp struct {
	cancel context.CancelFunc
	before func()
	early  bool
}

func (h *hangUp) RoundTrip(r *http.Request) (*http.Response, error) {
	if h.early {
		h.cancel()
		<-r.Context().Done()
		return nil, r.Context().Err()
	}
	resp, err := http.DefaultTransport.RoundTrip(r.WithContext(context.WithoutCancel(r.Context())))
	if err != nil {
		return nil, err
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	if h.before != nil {
		h.before()
	}
	h.cancel()
	return resp, nil
}

// requesterCtx is a cancellable request context carrying the hang-up
// transport as oauth2's HTTP client (kept by outbound.WithIdentityClient),
// with a session guard for user when guarded.
func requesterCtx(t *testing.T, user uuid.UUID, guarded bool, h *hangUp) context.Context {
	t.Helper()
	ctx := context.Background()
	if guarded {
		ctx = storage.WithSessionGuard(ctx, &storage.SessionGuard{UserID: user, Gen: 0})
	}
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	h.cancel = cancel
	return context.WithValue(ctx, oauth2.HTTPClient, &http.Client{Transport: h})
}

// AC-14 (current session), AC-15 (no session): a permanent refusal answered
// as the requester hangs up is recorded: one loss, one entry, Recorded.
func TestPAC80_LossIsRecordedAfterAHangUp(t *testing.T) {
	for _, p := range providers {
		for _, guarded := range []bool{true, false} {
			name := p + "/session"
			if !guarded {
				name = p + "/background"
			}
			t.Run(name, func(t *testing.T) {
				db := openTestDB(t)
				d := oauthfake.New()
				defer d.Close()
				d.Answer("a-refresh", oauthfake.InvalidGrant)
				u := seedUser(t, db, "pac80-hangup-loss@example.com")
				seedPAC80(t, db, p, u, "a-old", "a-refresh")
				h := &hangUp{}
				ctx := requesterCtx(t, u, guarded, h)
				_, err := sourceFor(t, ctx, db, db, d, p, u).Token()
				var lost *LostError
				if !errors.As(err, &lost) || !lost.Recorded {
					t.Fatalf("err = %v, want a LostError marked recorded", err)
				}
				if ctx.Err() == nil {
					t.Fatal("the requester did not hang up")
				}
				if l, _ := storage.IsCredentialLost(db, u, p); !l {
					t.Error("the loss is not recorded")
				}
				if e := auditOf(t, db, u); len(e) != 1 || e[0].action != "connection_lost" {
					t.Errorf("entries = %+v, want one connection_lost", e)
				}
			})
		}
	}
}

// AC-16: the session goes stale before the provider answers: the finishing
// step keeps the re-check, so nothing is recorded.
func TestPAC80_HangUpFinishingStepKeepsTheReCheck(t *testing.T) {
	for _, p := range providers {
		t.Run(p, func(t *testing.T) {
			db := openTestDB(t)
			d := oauthfake.New()
			defer d.Close()
			d.Answer("a-refresh", oauthfake.InvalidGrant)
			u := seedUser(t, db, "pac80-hangup-stale@example.com")
			seedPAC80(t, db, p, u, "a-old", "a-refresh")
			h := &hangUp{before: func() {
				if _, err := db.Exec(`UPDATE users SET session_generation = session_generation + 1 WHERE id = $1`, u); err != nil {
					t.Error(err)
				}
			}}
			_, err := sourceFor(t, requesterCtx(t, u, true, h), db, db, d, p, u).Token()
			var lost *LostError
			if !errors.As(err, &lost) || lost.Recorded {
				t.Fatalf("err = %v, want a LostError not recorded", err)
			}
			assertNothingRecorded(t, db, p, u)
		})
	}
}

// AC-17: a rotating provider answers a renewal as the requester hangs up:
// the new refresh token is stored.
func TestPAC80_RotatedRenewalIsStoredAfterAHangUp(t *testing.T) {
	for _, p := range providers {
		t.Run(p, func(t *testing.T) {
			db := openTestDB(t)
			d := oauthfake.New()
			defer d.Close()
			d.Rotate(true)
			u := seedUser(t, db, "pac80-hangup-rotate@example.com")
			seedPAC80(t, db, p, u, "a-old", "a-refresh")
			h := &hangUp{}
			tok, err := sourceFor(t, requesterCtx(t, u, true, h), db, db, d, p, u).Token()
			if err != nil {
				t.Fatal(err)
			}
			if tok.RefreshToken == "a-refresh" {
				t.Fatal("the provider did not rotate")
			}
			if got := stored(t, db, p, u); got == nil || got.RefreshToken != tok.RefreshToken || got.AccessToken != tok.AccessToken {
				t.Fatalf("stored = %+v, want the rotated %q", got, tok.RefreshToken)
			}
		})
	}
}

// hangingDB answers like the test database until hang is set, then blocks
// every call until its context ends (a database that never answers).
type hangingDB struct {
	*sql.DB
	hang atomic.Bool
}

func (h *hangingDB) wait(ctx context.Context) {
	if h.hang.Load() {
		<-ctx.Done()
	}
}

func (h *hangingDB) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	h.wait(ctx)
	return h.DB.ExecContext(ctx, q, args...)
}

func (h *hangingDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	h.wait(ctx)
	return h.DB.QueryContext(ctx, q, args...)
}

func (h *hangingDB) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	h.wait(ctx)
	return h.DB.QueryRowContext(ctx, q, args...)
}

func (h *hangingDB) BeginTx(ctx context.Context, o *sql.TxOptions) (*sql.Tx, error) {
	h.wait(ctx)
	return h.DB.BeginTx(ctx, o)
}

// The context-less calls are reached only through storage.Bind, which
// supplies the context: they are not used on these paths.
func (h *hangingDB) Exec(q string, args ...any) (sql.Result, error) {
	return h.ExecContext(context.Background(), q, args...)
}

func (h *hangingDB) Query(q string, args ...any) (*sql.Rows, error) {
	return h.QueryContext(context.Background(), q, args...)
}

func (h *hangingDB) QueryRow(q string, args ...any) *sql.Row {
	return h.QueryRowContext(context.Background(), q, args...)
}

func (h *hangingDB) Begin() (*sql.Tx, error) { return h.BeginTx(context.Background(), nil) }

// AC-18: the finishing step against a database that never answers returns
// within the bound plus one second, and its log line has no token material.
func TestPAC80_FinishingStepIsBounded(t *testing.T) {
	if finishBound != 5*time.Second {
		t.Fatalf("finishBound = %v, want the spec's 5 s (D-6)", finishBound)
	}
	prev := finishBound
	finishBound = time.Second // shortened for the run; the rule is the bound, not its size
	t.Cleanup(func() { finishBound = prev })
	var logs bytes.Buffer
	var logMu sync.Mutex
	prevOut := log.Writer()
	log.SetOutput(lockedWriter{&logMu, &logs})
	t.Cleanup(func() { log.SetOutput(prevOut) })

	for _, outcome := range []string{"loss", "save"} {
		for _, guarded := range []bool{true, false} {
			name := outcome + "/session"
			if !guarded {
				name = outcome + "/background"
			}
			t.Run(name, func(t *testing.T) {
				raw := openTestDB(t)
				d := oauthfake.New()
				defer d.Close()
				if outcome == "loss" {
					d.Answer("a-secret-refresh", oauthfake.InvalidGrant)
				} else {
					d.Rotate(true)
				}
				u := seedUser(t, raw, "pac80-bound@example.com")
				seed(t, raw, storage.CredentialGoogle, u, "a-secret-access", "a-secret-refresh", past)
				db := &hangingDB{DB: raw}
				h := &hangUp{before: func() { db.hang.Store(true) }}
				src := sourceFor(t, requesterCtx(t, u, guarded, h), db, raw, d, storage.CredentialGoogle, u)
				start := time.Now()
				tok, err := src.Token()
				if took := time.Since(start); took > finishBound+time.Second {
					t.Fatalf("Token() took %v, want at most the bound %v plus 1 s", took, finishBound)
				}
				if outcome == "loss" {
					var lost *LostError
					if !errors.As(err, &lost) || lost.Recorded {
						t.Fatalf("err = %v, want a LostError not recorded", err)
					}
				} else if err != nil || tok == nil {
					t.Fatalf("Token() = %v, %v; the renewal is still returned", tok, err)
				}
				logMu.Lock()
				out := logs.String()
				logs.Reset()
				logMu.Unlock()
				if !strings.Contains(out, "credential: ") {
					t.Fatalf("the failed finishing step was not logged: %q", out)
				}
				for _, secret := range []string{"a-secret", "access_token", "refresh_token"} {
					if strings.Contains(out, secret) {
						t.Fatalf("the log carries token material %q: %q", secret, out)
					}
				}
				if tok != nil && strings.Contains(out, tok.AccessToken) {
					t.Fatalf("the log carries the renewed token: %q", out)
				}
			})
		}
	}
}

type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// AC-19: the requester hangs up BEFORE the provider answers: a temporary
// failure, nothing recorded (as today).
func TestPAC80_HangUpBeforeTheAnswerRecordsNothing(t *testing.T) {
	for _, p := range providers {
		t.Run(p, func(t *testing.T) {
			db := openTestDB(t)
			d := oauthfake.New()
			defer d.Close()
			d.Answer("a-refresh", oauthfake.InvalidGrant)
			u := seedUser(t, db, "pac80-hangup-early@example.com")
			seedPAC80(t, db, p, u, "a-old", "a-refresh")
			h := &hangUp{early: true}
			_, err := sourceFor(t, requesterCtx(t, u, true, h), db, db, d, p, u).Token()
			if err == nil || errors.Is(err, ErrConnectionLost) {
				t.Fatalf("err = %v, want a temporary failure", err)
			}
			assertNothingRecorded(t, db, p, u)
		})
	}
}
