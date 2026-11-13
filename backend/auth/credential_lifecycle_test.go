package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/internal/oauthfake"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// PAC-67: renewal, write-back and loss recording for stored Google, Outlook
// and Zoom credentials. The provider is oauthfake, a test double.

var (
	past   = time.Now().Add(-time.Hour)
	future = time.Now().Add(time.Hour)
)

func googleCfg(d *oauthfake.Server) *oauth2.Config {
	return &oauth2.Config{ClientID: "g", ClientSecret: "gs", Endpoint: oauth2.Endpoint{AuthURL: d.URL + "/auth", TokenURL: d.TokenURL()}}
}

func msCfg(d *oauthfake.Server) *MicrosoftConfig {
	return &MicrosoftConfig{ClientID: "m", ClientSecret: "ms", Endpoint: oauth2.Endpoint{AuthURL: d.URL + "/auth", TokenURL: d.TokenURL()}}
}

func zoomCfg(d *oauthfake.Server) *oauth2.Config {
	return &oauth2.Config{ClientID: "z", ClientSecret: "zs", Endpoint: oauth2.Endpoint{TokenURL: d.TokenURL(), AuthStyle: oauth2.AuthStyleInHeader}}
}

// storeOutlook stores a Microsoft credential directly (no sign-in, so the
// calendar provider is not touched).
func storeOutlook(t *testing.T, db *sql.DB, user uuid.UUID, access, refresh string, expiry time.Time) {
	t.Helper()
	data, _ := json.Marshal(microsoftTokenRecord{AccessToken: access, RefreshToken: refresh, Expiry: expiry.UTC()})
	if _, err := db.Exec(`INSERT INTO settings (user_id, microsoft_tokens) VALUES ($1, $2) ON CONFLICT (user_id) DO UPDATE SET microsoft_tokens = EXCLUDED.microsoft_tokens`, user, string(data)); err != nil {
		t.Fatal(err)
	}
}

func storeZoom(t *testing.T, db *sql.DB, user uuid.UUID, access, refresh string, expiry *time.Time) {
	t.Helper()
	if err := storage.SaveZoomCredential(db, user, storage.ZoomCredential{AccessToken: access, RefreshToken: refresh, Expiry: expiry}); err != nil {
		t.Fatal(err)
	}
}

// source builds the renewing token source for provider over user's stored credential.
func source(t *testing.T, db *sql.DB, d *oauthfake.Server, provider string, user uuid.UUID) oauth2.TokenSource {
	t.Helper()
	ctx := context.Background()
	switch provider {
	case storage.CredentialGoogle:
		tok, err := LoadUserToken(db, user)
		if err != nil || tok == nil {
			t.Fatalf("load google: %v %v", tok, err)
		}
		return UserTokenSource(ctx, db, googleCfg(d), user, tok)
	case storage.CredentialOutlook:
		tok, err := LoadMicrosoftToken(db, user)
		if err != nil || tok == nil {
			t.Fatalf("load outlook: %v %v", tok, err)
		}
		return MicrosoftTokenSource(ctx, db, msCfg(d), user, tok)
	default:
		cred, err := storage.LoadZoomCredential(db, user)
		if err != nil || cred == nil {
			t.Fatalf("load zoom: %v %v", cred, err)
		}
		return ZoomTokenSource(ctx, db, zoomCfg(d), user, cred)
	}
}

// seed stores an expired credential with refresh token rt for provider.
func seed(t *testing.T, db *sql.DB, provider string, user uuid.UUID, access, rt string, expiry time.Time) {
	t.Helper()
	switch provider {
	case storage.CredentialGoogle:
		if err := UpsertUserToken(db, user, &oauth2.Token{AccessToken: access, RefreshToken: rt, Expiry: expiry}); err != nil {
			t.Fatal(err)
		}
	case storage.CredentialOutlook:
		storeOutlook(t, db, user, access, rt, expiry)
	default:
		storeZoom(t, db, user, access, rt, &expiry)
	}
}

func stored(t *testing.T, db *sql.DB, provider string, user uuid.UUID) *oauth2.Token {
	t.Helper()
	switch provider {
	case storage.CredentialGoogle:
		tok, _ := LoadUserToken(db, user)
		return tok
	case storage.CredentialOutlook:
		tok, _ := LoadMicrosoftToken(db, user)
		return tok
	default:
		c, _ := storage.LoadZoomCredential(db, user)
		if c == nil {
			return nil
		}
		tok := &oauth2.Token{AccessToken: c.AccessToken, RefreshToken: c.RefreshToken}
		if c.Expiry != nil {
			tok.Expiry = *c.Expiry
		}
		return tok
	}
}

type auditRow struct{ action, details string }

func auditOf(t *testing.T, db *sql.DB, user uuid.UUID) []auditRow {
	t.Helper()
	rows, err := db.Query(`SELECT action, details FROM audit_log WHERE user_id = $1 ORDER BY id`, user)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []auditRow
	for rows.Next() {
		var r auditRow
		if err := rows.Scan(&r.action, &r.details); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

var providers = []string{storage.CredentialGoogle, storage.CredentialOutlook, storage.CredentialZoom}

// AC-1, AC-2, AC-3, AC-6, AC-27: an expired credential is renewed with its
// owner's refresh token, the renewal is stored for the owner only, and a
// second action within the new lifetime sends no renewal.
func TestRenewal_StoredForOwnerOnly(t *testing.T) {
	for _, p := range providers {
		t.Run(p, func(t *testing.T) {
			db := openTestDB(t)
			d := oauthfake.New()
			defer d.Close()
			alice := seedUser(t, db, "pac67-renew-alice@example.com")
			bob := seedUser(t, db, "pac67-renew-bob@example.com")
			seed(t, db, p, alice, "a-old", "a-refresh", past)
			seed(t, db, p, bob, "b-old", "b-refresh", past)
			bobBefore := stored(t, db, p, bob)

			tok, err := source(t, db, d, p, alice).Token()
			if err != nil {
				t.Fatalf("Token: %v", err)
			}
			calls := d.Calls("/token")
			if len(calls) != 1 || calls[0].Form.Get("refresh_token") != "a-refresh" {
				t.Fatalf("renewal requests = %+v, want one with alice's refresh token", calls)
			}
			if tok.AccessToken != "access-1" {
				t.Errorf("action used %q, want the renewed access-1", tok.AccessToken)
			}
			got := stored(t, db, p, alice)
			if got.AccessToken != "access-1" || got.RefreshToken != "a-refresh" || !got.Expiry.After(time.Now()) {
				t.Errorf("alice's stored credential = %+v, want access-1 / a-refresh / future expiry", got)
			}
			if after := stored(t, db, p, bob); *after != *bobBefore || d.Saw("b-refresh") || d.Saw("b-old") {
				t.Errorf("bob's credential changed or was sent: %+v -> %+v", bobBefore, after)
			}

			// AC-6, AC-7: a second action inside the new lifetime renews nothing.
			if _, err := source(t, db, d, p, alice).Token(); err != nil {
				t.Fatal(err)
			}
			if n := len(d.Calls("/token")); n != 1 {
				t.Errorf("renewal requests after a second action = %d, want 1", n)
			}
		})
	}
}

// AC-7: an unexpired access token is used as is.
func TestRenewal_UnexpiredTokenSendsNoRequest(t *testing.T) {
	for _, p := range providers {
		t.Run(p, func(t *testing.T) {
			db := openTestDB(t)
			d := oauthfake.New()
			defer d.Close()
			alice := seedUser(t, db, "pac67-fresh@example.com")
			seed(t, db, p, alice, "a-live", "a-refresh", future)
			tok, err := source(t, db, d, p, alice).Token()
			if err != nil || tok.AccessToken != "a-live" {
				t.Fatalf("Token = %v, %v", tok, err)
			}
			if n := len(d.Calls("")); n != 0 {
				t.Errorf("requests = %d, want 0", n)
			}
		})
	}
}

// AC-4: a rotated refresh token is stored and used by the next renewal.
func TestRenewal_RotationIsKept(t *testing.T) {
	for _, p := range providers {
		t.Run(p, func(t *testing.T) {
			db := openTestDB(t)
			d := oauthfake.New()
			defer d.Close()
			d.Rotate(true)
			alice := seedUser(t, db, "pac67-rotate@example.com")
			seed(t, db, p, alice, "a-old", "a-refresh", past)
			if _, err := source(t, db, d, p, alice).Token(); err != nil {
				t.Fatal(err)
			}
			got := stored(t, db, p, alice)
			if got.RefreshToken != "refresh-1" {
				t.Fatalf("stored refresh token = %q, want the rotated refresh-1", got.RefreshToken)
			}
			// The new access token expires too.
			seed(t, db, p, alice, got.AccessToken, got.RefreshToken, past)
			tok, err := source(t, db, d, p, alice).Token()
			if err != nil || tok.AccessToken != "access-2" {
				t.Fatalf("second renewal = %v, %v", tok, err)
			}
			if last := d.Calls("/token")[1].Form.Get("refresh_token"); last != "refresh-1" {
				t.Errorf("second renewal used %q, want refresh-1", last)
			}
		})
	}
}

// AC-5: concurrent actions in one process share one renewal even when the
// provider rotates refresh tokens and refuses a reused one.
func TestRenewal_ConcurrentActionsWithRotation(t *testing.T) {
	for _, p := range providers {
		t.Run(p, func(t *testing.T) {
			db := openTestDB(t)
			d := oauthfake.New()
			defer d.Close()
			d.Rotate(true)
			alice := seedUser(t, db, "pac67-concurrent@example.com")
			seed(t, db, p, alice, "a-old", "a-refresh", past)

			const n = 4
			var wg sync.WaitGroup
			errs := make([]error, n)
			for i := 0; i < n; i++ {
				src := source(t, db, d, p, alice)
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					_, errs[i] = src.Token()
				}(i)
			}
			wg.Wait()
			for i, err := range errs {
				if err != nil {
					t.Errorf("action %d failed: %v", i, err)
				}
			}
			// A third action, after the access token expires again, still works.
			got := stored(t, db, p, alice)
			seed(t, db, p, alice, got.AccessToken, got.RefreshToken, past)
			if _, err := source(t, db, d, p, alice).Token(); err != nil {
				t.Errorf("third action: %v", err)
			}
			if lost, _ := storage.IsCredentialLost(db, alice, p); lost {
				t.Error("a concurrent renewal was recorded as a loss")
			}
		})
	}
}

// AC-8(c), AC-9, AC-25: a permanent refusal is a loss of the owner's
// credential, announced by exactly one connection_lost entry however many
// actions, concurrent or not, hit it. B is untouched.
func TestPermanentRefusal_OneLossOneEntry(t *testing.T) {
	for _, p := range providers {
		t.Run(p, func(t *testing.T) {
			db := openTestDB(t)
			d := oauthfake.New()
			defer d.Close()
			d.Answer("a-refresh", oauthfake.InvalidGrant)
			alice := seedUser(t, db, "pac67-refused-alice@example.com")
			bob := seedUser(t, db, "pac67-refused-bob@example.com")
			seed(t, db, p, alice, "a-old", "a-refresh", past)
			seed(t, db, p, bob, "b-live", "b-refresh", future)

			var wg sync.WaitGroup
			errs := make([]error, 5)
			for i := range errs {
				src := source(t, db, d, p, alice)
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					_, errs[i] = src.Token()
				}(i)
			}
			wg.Wait()
			for i := 0; i < 3; i++ { // AC-9: three more actions
				_, err := source(t, db, d, p, alice).Token()
				errs = append(errs, err)
			}
			for i, err := range errs {
				var lost *LostError
				if !errors.As(err, &lost) || !errors.Is(err, ErrConnectionLost) || lost.Provider != p {
					t.Errorf("action %d: err = %v, want a LostError for %s", i, err, p)
				}
			}
			if lost, _ := storage.IsCredentialLost(db, alice, p); !lost {
				t.Error("the loss is not recorded")
			}
			entries := auditOf(t, db, alice)
			want := fmt.Sprintf(`{"provider":%q}`, p)
			if len(entries) != 1 || entries[0].action != "connection_lost" || entries[0].details != want {
				t.Errorf("alice's log = %+v, want one connection_lost %s", entries, want)
			}
			if got := stored(t, db, p, alice); got == nil || got.RefreshToken != "a-refresh" {
				t.Errorf("a refused credential was not kept (D-1): %+v", got)
			}
			if len(auditOf(t, db, bob)) != 0 {
				t.Error("bob's log changed")
			}
			if lost, _ := storage.IsCredentialLost(db, bob, p); lost {
				t.Error("bob reported lost")
			}
		})
	}
}

// AC-10: a temporary failure is neither a loss nor an entry.
func TestTemporaryFailure_RecordsNothing(t *testing.T) {
	for _, p := range providers {
		t.Run(p, func(t *testing.T) {
			db := openTestDB(t)
			d := oauthfake.New()
			defer d.Close()
			d.Answer("a-refresh", oauthfake.Unavailable)
			alice := seedUser(t, db, "pac67-flaky@example.com")
			seed(t, db, p, alice, "a-old", "a-refresh", past)
			_, err := source(t, db, d, p, alice).Token()
			if err == nil || errors.Is(err, ErrConnectionLost) {
				t.Fatalf("err = %v, want a temporary failure", err)
			}
			if lost, _ := storage.IsCredentialLost(db, alice, p); lost {
				t.Error("a temporary failure was recorded as a loss")
			}
			if n := len(auditOf(t, db, alice)); n != 0 {
				t.Errorf("entries = %d, want 0", n)
			}
		})
	}
}

// AC-24: loss, reconnect, second loss: two entries; the reconnect ended the first.
func TestLoss_ReconnectStartsANewEpisode(t *testing.T) {
	db := openTestDB(t)
	d := oauthfake.New()
	defer d.Close()
	alice := seedUser(t, db, "pac67-episodes@example.com")
	ctx := context.Background()
	reconnect := map[string]func(rt string){
		storage.CredentialGoogle: func(rt string) {
			if err := UpsertUserToken(db, alice, &oauth2.Token{AccessToken: "x", RefreshToken: rt, Expiry: past}); err != nil {
				t.Fatal(err)
			}
		},
		storage.CredentialOutlook: func(rt string) {
			if err := SaveMicrosoftToken(db, alice, &oauth2.Token{AccessToken: "x", RefreshToken: rt, Expiry: past}); err != nil {
				t.Fatal(err)
			}
		},
		storage.CredentialZoom: func(rt string) {
			if err := storage.SaveZoomCredential(db, alice, storage.ZoomCredential{AccessToken: "x", RefreshToken: rt, Expiry: &past}); err != nil {
				t.Fatal(err)
			}
		},
	}
	for _, p := range providers {
		d.Answer(p+"-1", oauthfake.InvalidGrant)
		d.Answer(p+"-2", oauthfake.InvalidGrant)
		reconnect[p](p + "-1")
		_, _ = source(t, db, d, p, alice).Token()
		reconnect[p](p + "-2")
		if lost, _ := storage.IsCredentialLost(db, alice, p); lost {
			t.Errorf("%s: a reconnect did not end the loss", p)
		}
		for i := 0; i < 2; i++ {
			_, _ = source(t, db, d, p, alice).Token()
		}
	}
	_ = ctx
	count := map[string]int{}
	for _, e := range auditOf(t, db, alice) {
		if e.action == "connection_lost" {
			count[e.details]++
		}
	}
	for _, p := range providers {
		if got := count[fmt.Sprintf(`{"provider":%q}`, p)]; got != 2 {
			t.Errorf("%s: connection_lost entries = %d, want 2 (one per loss)", p, got)
		}
	}
}

// A refusal of a refresh token that a reconnect has already replaced is not a
// loss of the new credential.
func TestRefusalOfAReplacedCredential_RecordsNoLoss(t *testing.T) {
	db := openTestDB(t)
	d := oauthfake.New()
	defer d.Close()
	d.Answer("old-refresh", oauthfake.InvalidGrant)
	alice := seedUser(t, db, "pac67-replaced@example.com")
	seed(t, db, storage.CredentialOutlook, alice, "a-old", "old-refresh", past)
	src := source(t, db, d, storage.CredentialOutlook, alice)
	// The user reconnects before the in-flight action renews.
	storeOutlook(t, db, alice, "a-new", "new-refresh", future)
	tok, err := src.Token()
	if err != nil || tok.AccessToken != "a-new" {
		t.Fatalf("Token = %v, %v; want the reconnected credential", tok, err)
	}
	if lost, _ := storage.IsCredentialLost(db, alice, storage.CredentialOutlook); lost {
		t.Error("the reconnected credential was reported lost")
	}
}

// CONTRACT-11: an expired Teams or Zoom credential with no refresh token
// cannot be renewed: no request, a LostError, nothing recorded.
func TestNoRefreshToken_LostWithoutRecording(t *testing.T) {
	for _, p := range []string{storage.CredentialOutlook, storage.CredentialZoom} {
		t.Run(p, func(t *testing.T) {
			db := openTestDB(t)
			d := oauthfake.New()
			defer d.Close()
			alice := seedUser(t, db, "pac67-norefresh@example.com")
			seed(t, db, p, alice, "a-old", "", past)
			_, err := source(t, db, d, p, alice).Token()
			var lost *LostError
			if !errors.As(err, &lost) || lost.Recorded {
				t.Fatalf("err = %v, want an unrecorded LostError", err)
			}
			if n := len(d.Calls("")); n != 0 {
				t.Errorf("requests = %d, want 0", n)
			}
			if l, _ := storage.IsCredentialLost(db, alice, p); l || len(auditOf(t, db, alice)) != 0 {
				t.Error("a credential that was never usable was recorded lost")
			}
		})
	}
}

// A Zoom credential stored before PAC-67 has no expiry: it is renewed, not used blind.
func TestZoom_NoStoredExpiryIsRenewed(t *testing.T) {
	db := openTestDB(t)
	d := oauthfake.New()
	defer d.Close()
	alice := seedUser(t, db, "pac67-zoom-noexpiry@example.com")
	if err := storage.SaveZoomTokens(db, alice, "a-old", "a-refresh"); err != nil {
		t.Fatal(err)
	}
	tok, err := source(t, db, d, storage.CredentialZoom, alice).Token()
	if err != nil || tok.AccessToken != "access-1" {
		t.Fatalf("Token = %v, %v; want a renewal", tok, err)
	}
	if c, _ := storage.LoadZoomCredential(db, alice); c.Expiry == nil {
		t.Error("the renewal stored no expiry")
	}
}

// Without a configured provider there is nothing to renew with: a temporary
// failure, never a loss.
func TestRenewal_UnconfiguredProviderIsTemporary(t *testing.T) {
	db := openTestDB(t)
	alice := seedUser(t, db, "pac67-unconfigured@example.com")
	storeOutlook(t, db, alice, "a-old", "a-refresh", past)
	tok, _ := LoadMicrosoftToken(db, alice)
	_, err := MicrosoftTokenSource(context.Background(), db, nil, alice, tok).Token()
	if err == nil || errors.Is(err, ErrConnectionLost) {
		t.Fatalf("err = %v, want a temporary failure", err)
	}
	if lost, _ := storage.IsCredentialLost(db, alice, storage.CredentialOutlook); lost {
		t.Error("recorded a loss")
	}
}

// AC-11, AC-12: a Microsoft reconnect without a refresh token keeps the stored
// one and ends a recorded loss; a Google reconnect ends its loss too, and a
// Google disconnect ends it.
func TestReconnect_KeepsRefreshTokenAndEndsLoss(t *testing.T) {
	db := openTestDB(t)
	alice := seedUser(t, db, "pac67-reconnect@example.com")
	if err := SaveMicrosoftToken(db, alice, &oauth2.Token{AccessToken: "a1", RefreshToken: "r1", Expiry: future}); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.RecordCredentialLoss(db, alice, storage.CredentialOutlook); err != nil {
		t.Fatal(err)
	}
	if err := SaveMicrosoftToken(db, alice, &oauth2.Token{AccessToken: "a2", Expiry: future}); err != nil {
		t.Fatal(err)
	}
	tok, _ := LoadMicrosoftToken(db, alice)
	if tok.AccessToken != "a2" || tok.RefreshToken != "r1" {
		t.Errorf("stored = %+v, want a2 with the kept r1", tok)
	}
	if lost, _ := storage.IsCredentialLost(db, alice, storage.CredentialOutlook); lost {
		t.Error("an Outlook reconnect did not end the loss")
	}

	if err := UpsertUserToken(db, alice, &oauth2.Token{AccessToken: "g1", RefreshToken: "gr1", Expiry: future}); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.RecordCredentialLoss(db, alice, storage.CredentialGoogle); err != nil {
		t.Fatal(err)
	}
	if err := UpsertUserToken(db, alice, &oauth2.Token{AccessToken: "g2", Expiry: future}); err != nil {
		t.Fatal(err)
	}
	g, _ := LoadUserToken(db, alice)
	if g.AccessToken != "g2" || g.RefreshToken != "gr1" {
		t.Errorf("google stored = %+v, want g2 with the kept gr1", g)
	}
	if lost, _ := storage.IsCredentialLost(db, alice, storage.CredentialGoogle); lost {
		t.Error("a Google reconnect did not end the loss")
	}
	if _, err := storage.RecordCredentialLoss(db, alice, storage.CredentialGoogle); err != nil {
		t.Fatal(err)
	}
	if err := DeleteUserToken(db, alice); err != nil {
		t.Fatal(err)
	}
	if lost, _ := storage.IsCredentialLost(db, alice, storage.CredentialGoogle); lost {
		t.Error("a Google disconnect did not end the loss")
	}
}

func TestIsPermanentRefusal(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"invalid_grant":         {&oauth2.RetrieveError{ErrorCode: "invalid_grant", Response: &http.Response{StatusCode: 400}}, true},
		"wrapped invalid_grant": {fmt.Errorf("refresh: %w", &oauth2.RetrieveError{ErrorCode: "invalid_grant"}), true},
		"server error":          {&oauth2.RetrieveError{Response: &http.Response{StatusCode: 503}}, false},
		"invalid_client":        {&oauth2.RetrieveError{ErrorCode: "invalid_client"}, false},
		"network":               {errors.New("dial tcp: connection refused"), false},
		"nil":                   {nil, false},
	} {
		if got := IsPermanentRefusal(tc.err); got != tc.want {
			t.Errorf("%s: IsPermanentRefusal = %v, want %v", name, got, tc.want)
		}
	}
	e := &LostError{Provider: "zoom", Recorded: true, Err: errors.New("x")}
	if !errors.Is(e, ErrConnectionLost) || e.Error() == "" || errors.Unwrap(e) == nil {
		t.Error("LostError does not behave as ErrConnectionLost")
	}
}
