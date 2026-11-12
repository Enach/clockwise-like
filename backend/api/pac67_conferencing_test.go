package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/internal/oauthfake"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// PAC-67 contract tests for meeting creation, the status surfaces, the two
// disconnects and the callbacks. Spec docs/specs/PAC-67.md, contract
// contracts/features/PAC-67.{md,yaml}.

// AC-3, AC-12 (success side): an expired Teams or Zoom access token is renewed
// and the meeting is created with the renewed token, standalone and on an
// existing event; the renewal is stored for the caller.
func TestPAC67_MeetingCreationRenewsExpiredCredential(t *testing.T) {
	for _, provider := range []string{"teams", "zoom"} {
		t.Run(provider, func(t *testing.T) {
			w := newPAC67World(t)
			alice := w.user("pac67-renew-" + provider + "@example.com")
			w.seedGoogle(alice, "g-live-"+provider, "g-r-"+provider, pac67Future)
			rt := "renew-r-" + provider
			if provider == "teams" {
				w.seedOutlook(alice, "ms-old", rt, pac67Past)
				w.providers(alice, "google", "teams")
			} else {
				w.seedZoom(alice, "zoom-old", rt, &pac67Past)
			}

			rec := w.do(alice, http.MethodPost, "/api/conference/create", pac67Meeting)
			if rec.Code != http.StatusOK {
				t.Fatalf("standalone = %d %s", rec.Code, rec.Body.String())
			}
			var details map[string]any
			_ = json.Unmarshal(rec.Body.Bytes(), &details)
			if details["provider"] != provider || details["joinUrl"] == "" {
				t.Errorf("standalone details = %v", details)
			}
			refreshes := w.d.Calls("/token")
			if len(refreshes) != 1 || refreshes[0].Form.Get("refresh_token") != rt {
				t.Fatalf("renewals = %+v, want one with %s", refreshes, rt)
			}
			api := w.d.Calls("")
			if last := api[len(api)-1]; last.Bearer != "access-1" {
				t.Errorf("meeting created with %q, want the renewed access-1", last.Bearer)
			}

			// The renewed token is stored: expire it again and add a meeting to an event.
			if provider == "teams" {
				tok, _ := auth.LoadMicrosoftToken(w.db, alice)
				if tok.AccessToken != "access-1" || tok.RefreshToken != rt {
					t.Fatalf("stored teams credential = %+v", tok)
				}
				w.seedOutlook(alice, "access-1", rt, pac67Past)
			} else {
				c, _ := storage.LoadZoomCredential(w.db, alice)
				if c.AccessToken != "access-1" || c.Expiry == nil || !c.Expiry.After(time.Now()) {
					t.Fatalf("stored zoom credential = %+v", c)
				}
				w.seedZoom(alice, "access-1", rt, &pac67Past)
			}
			rec = w.do(alice, http.MethodPost, "/api/events/e1/conference", `{"provider":"`+provider+`"}`)
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"provider":"`+provider+`"`) {
				t.Fatalf("on-event = %d %s", rec.Code, rec.Body.String())
			}
			api = w.d.Calls("")
			if last := api[len(api)-1]; last.Bearer != "access-2" {
				t.Errorf("on-event meeting created with %q, want access-2", last.Bearer)
			}
			if !w.cal.saw("g-live-" + provider) {
				t.Error("the event was not read with the caller's Google credential")
			}
			w.assertEntries(alice)
		})
	}
}

// AC-7: an unexpired token is used without a renewal request.
func TestPAC67_UnexpiredCredentialIsNotRenewed(t *testing.T) {
	w := newPAC67World(t)
	alice := w.user("pac67-unexpired@example.com")
	w.seedZoom(alice, "zoom-live", "zoom-live-r", &pac67Future)
	if rec := w.do(alice, http.MethodPost, "/api/conference/create", pac67Meeting); rec.Code != http.StatusOK {
		t.Fatalf("create = %d %s", rec.Code, rec.Body.String())
	}
	if n := len(w.d.Calls("/token")); n != 0 {
		t.Errorf("renewal requests = %d, want 0", n)
	}
}

// AC-8 (Teams and Zoom rows), AC-9, AC-21 (first half): a permanent refusal
// fails meeting creation with the reconnect sentence, reports the provider not
// connected, writes one connection_lost entry, and leaves Bob alone.
func TestPAC67_PermanentRefusalOnMeetingCreation(t *testing.T) {
	for _, tc := range []struct{ provider, credential, sentence string }{
		{"teams", storage.CredentialOutlook, teamsLost},
		{"zoom", storage.CredentialZoom, zoomLost},
	} {
		for _, path := range []string{"standalone", "on-event"} {
			t.Run(tc.provider+"/"+path, func(t *testing.T) {
				w := newPAC67World(t)
				tag := tc.provider + "-" + path
				alice := w.user("pac67-refused-a-" + tag + "@example.com")
				bob := w.user("pac67-refused-b-" + tag + "@example.com")
				dead := "dead-r-" + tag
				w.d.Answer(dead, oauthfake.InvalidGrant)
				for _, u := range []uuid.UUID{alice, bob} {
					w.seedGoogle(u, "g-"+u.String(), "gr-"+u.String(), pac67Future)
				}
				if tc.provider == "teams" {
					// AC-21: Alice's calendar is Google, Teams uses her Outlook credential.
					w.providers(alice, "google", "teams")
					w.seedOutlook(alice, "ms-old", dead, pac67Past)
					w.providers(bob, "outlook", "teams")
					w.seedOutlook(bob, "ms-bob", "bob-r-"+tag, pac67Future)
				} else {
					w.seedZoom(alice, "zoom-old", dead, &pac67Past)
					w.seedZoom(bob, "zoom-bob", "bob-r-"+tag, &pac67Future)
				}
				bobRow, bobStatus := w.row(bob, tc.provider), w.status(bob)

				act := func() *httptest.ResponseRecorder {
					if path == "standalone" {
						return w.do(alice, http.MethodPost, "/api/conference/create", pac67Meeting)
					}
					return w.do(alice, http.MethodPost, "/api/events/e1/conference", `{"provider":"`+tc.provider+`"}`)
				}
				assertBody(t, act(), http.StatusBadRequest, tc.sentence)
				w.assertRow(alice, tc.provider, false)
				if tc.provider == "teams" {
					if s := w.status(alice); s["provider"] != "google" || s["connected"] != true {
						t.Errorf("AC-21: alice's calendar status = %v, want google connected", s)
					}
				}
				want := fmt.Sprintf(`connection_lost {"provider":%q}`, tc.credential)
				w.assertEntries(alice, want)

				// AC-9: three more actions on the same dead credential.
				for i := 0; i < 3; i++ {
					assertBody(t, act(), http.StatusBadRequest, tc.sentence)
				}
				w.assertEntries(alice, want)
				if !w.lost(alice, tc.credential) {
					t.Error("the loss is not recorded")
				}
				if w.cal.puts != 0 {
					t.Error("a failed meeting creation wrote to the event")
				}

				// (d) Bob is unchanged.
				if r := w.row(bob, tc.provider); fmt.Sprint(r) != fmt.Sprint(bobRow) {
					t.Errorf("bob's %s row changed: %v -> %v", tc.provider, bobRow, r)
				}
				if s := w.status(bob); fmt.Sprint(s) != fmt.Sprint(bobStatus) {
					t.Errorf("bob's status changed: %v -> %v", bobStatus, s)
				}
				w.assertEntries(bob)
			})
		}
	}
}

// AC-25: concurrent refused actions in one process write one entry.
func TestPAC67_ConcurrentRefusalsWriteOneEntry(t *testing.T) {
	w := newPAC67World(t)
	alice := w.user("pac67-concurrent-refusal@example.com")
	w.d.Answer("conc-dead", oauthfake.InvalidGrant)
	w.seedZoom(alice, "zoom-old", "conc-dead", &pac67Past)
	var wg sync.WaitGroup
	codes := make([]int, 6)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = w.do(alice, http.MethodPost, "/api/conference/create", pac67Meeting).Code
		}(i)
	}
	wg.Wait()
	for i, c := range codes {
		if c != http.StatusBadRequest {
			t.Errorf("action %d = %d, want 400", i, c)
		}
	}
	w.assertRow(alice, "zoom", false)
	w.assertEntries(alice, `connection_lost {"provider":"zoom"}`)
}

// AC-10: a temporary renewal failure never says reconnect, keeps the provider
// connected and writes nothing: standalone 500, on-event 502.
func TestPAC67_TemporaryFailureOnMeetingCreation(t *testing.T) {
	for _, provider := range []string{"teams", "zoom"} {
		t.Run(provider, func(t *testing.T) {
			w := newPAC67World(t)
			alice := w.user("pac67-temporary-" + provider + "@example.com")
			flaky := "flaky-r-" + provider
			w.d.Answer(flaky, oauthfake.Unavailable)
			w.seedGoogle(alice, "g-tmp-"+provider, "gr-tmp-"+provider, pac67Future)
			if provider == "teams" {
				w.providers(alice, "outlook", "teams")
				w.seedOutlook(alice, "ms-old", flaky, pac67Past)
			} else {
				w.seedZoom(alice, "zoom-old", flaky, &pac67Past)
			}
			assertBody(t, w.do(alice, http.MethodPost, "/api/conference/create", pac67Meeting), http.StatusInternalServerError, `{"error":"internal error"}`)
			assertBody(t, w.do(alice, http.MethodPost, "/api/events/e1/conference", `{"provider":"`+provider+`"}`), http.StatusBadGateway, `{"error":"upstream provider error"}`)
			w.assertRow(alice, provider, true)
			if provider == "teams" {
				if s := w.status(alice); s["connected"] != true {
					t.Errorf("outlook status = %v, want connected", s)
				}
			}
			w.assertEntries(alice)
		})
	}
}

// AC-23, AC-8 Google on-event row, D-10: Google refused on the on-event path is
// the 404; the Teams or Zoom credential is neither sent nor reported.
func TestPAC67_GoogleRefusedOnEventNeverReachesTeamsOrZoom(t *testing.T) {
	for _, provider := range []string{"teams", "zoom"} {
		t.Run(provider, func(t *testing.T) {
			w := newPAC67World(t)
			alice := w.user("pac67-ac23-" + provider + "@example.com")
			w.d.Answer("g-dead-"+provider, oauthfake.InvalidGrant)
			w.seedGoogle(alice, "g-old", "g-dead-"+provider, pac67Past)
			// The Teams or Zoom credential is expired and would be refused too.
			other := "other-dead-" + provider
			w.d.Answer(other, oauthfake.InvalidGrant)
			if provider == "teams" {
				w.providers(alice, "google", "teams")
				w.seedOutlook(alice, "ms-old-"+provider, other, pac67Past)
			} else {
				w.providers(alice, "google", "zoom")
				w.seedZoom(alice, "zoom-old-"+provider, other, &pac67Past)
			}
			assertBody(t, w.do(alice, http.MethodPost, "/api/events/e1/conference", `{"provider":"`+provider+`"}`), http.StatusNotFound, `{"error":"event not found"}`)
			if w.d.Saw(other) || w.d.Saw("ms-old-"+provider) || w.d.Saw("zoom-old-"+provider) {
				t.Error("a request carried the Teams or Zoom credential")
			}
			w.assertRow(alice, "google_meet", false)
			if s := w.status(alice); s["provider"] != "google" || s["connected"] != false {
				t.Errorf("calendar status = %v, want google not connected", s)
			}
			w.assertRow(alice, provider, true)
			w.assertEntries(alice, `connection_lost {"provider":"google"}`)
		})
	}
}

// AC-12, AC-24: a reconnect makes the surfaces connected and creation work
// again; a later refusal of the new credential is a second loss.
func TestPAC67_ReconnectAfterLossAndSecondLoss(t *testing.T) {
	w := newPAC67World(t)
	alice := w.user("pac67-reconnect-zoom@example.com")
	w.d.Answer("first-dead", oauthfake.InvalidGrant)
	w.seedZoom(alice, "zoom-old", "first-dead", &pac67Past)
	assertBody(t, w.do(alice, http.MethodPost, "/api/conference/create", pac67Meeting), http.StatusBadRequest, zoomLost)
	w.assertRow(alice, "zoom", false)

	// Reconnect through the Zoom callback.
	w.d.ExchangeAnswer("zoom-new-access", "second-r")
	if rec := pac67ZoomCallback(w, alice); rec.Code != http.StatusFound {
		t.Fatalf("zoom callback = %d %s", rec.Code, rec.Body.String())
	}
	w.assertRow(alice, "zoom", true)
	if rec := w.do(alice, http.MethodPost, "/api/conference/create", pac67Meeting); rec.Code != http.StatusOK {
		t.Fatalf("after reconnect = %d %s", rec.Code, rec.Body.String())
	}
	api := w.d.Calls("/api/users/me/meetings")
	if last := api[len(api)-1]; last.Bearer != "zoom-new-access" {
		t.Errorf("meeting created with %q, want the reconnected token", last.Bearer)
	}

	// The new credential dies too: a second loss, a second entry, and no more.
	w.d.Answer("second-r", oauthfake.InvalidGrant)
	w.seedZoom(alice, "zoom-new-access", "second-r", &pac67Past)
	for i := 0; i < 3; i++ {
		assertBody(t, w.do(alice, http.MethodPost, "/api/conference/create", pac67Meeting), http.StatusBadRequest, zoomLost)
	}
	w.assertRow(alice, "zoom", false)
	w.assertEntries(alice, `connection_lost {"provider":"zoom"}`, `connection_lost {"provider":"zoom"}`)
}

func pac67ZoomCallback(w *pac67World, user uuid.UUID) *httptest.ResponseRecorder {
	req := withUser(httptest.NewRequest(http.MethodGet, "/api/auth/zoom/callback?state=s1&code=c1", nil), user)
	req.AddCookie(&http.Cookie{Name: "zoom_oauth_state", Value: "s1"})
	rec := httptest.NewRecorder()
	w.ch.zoomCallback(rec, req)
	return rec
}

// AC-11 (Zoom): a reconnect whose exchange returns no refresh token keeps the
// stored one; the expiry is stored.
func TestPAC67_ZoomCallbackKeepsRefreshTokenAndStoresExpiry(t *testing.T) {
	w := newPAC67World(t)
	alice := w.user("pac67-zoom-callback@example.com")
	w.seedZoom(alice, "zoom-a1", "zoom-kept-r", &pac67Future)
	w.d.ExchangeAnswer("zoom-a2", "")
	if rec := pac67ZoomCallback(w, alice); rec.Code != http.StatusFound {
		t.Fatalf("callback = %d %s", rec.Code, rec.Body.String())
	}
	c, _ := storage.LoadZoomCredential(w.db, alice)
	if c.AccessToken != "zoom-a2" || c.RefreshToken != "zoom-kept-r" || c.Expiry == nil || c.Expiry.Before(time.Now().Add(30*time.Minute)) {
		t.Errorf("stored = %+v, want zoom-a2, kept refresh, expiry about an hour away", c)
	}
	w.assertRow(alice, "zoom", true)
}

// AC-11 (Outlook) and AC-19: a Microsoft reconnect without a refresh token
// keeps the stored one; only the caller's account changes.
//
// PAC-71 rewrite (spec §6 "Must change", owner PAC-47 vs PAC-67 OQ-13): a
// session-less Microsoft callback matched by address is now a refused sign-in,
// so the reconnect is the signed-in CONNECT (AC-12 shape). Alice chose Outlook
// as her work calendar in the Settings control before connecting, as that
// control does; the connect itself never changes the work calendar (D-15).
// Every PAC-67 assertion is kept: refresh token kept, loss ended, Alice's
// status Outlook-connected, Bob unaffected.
func TestPAC67_MicrosoftConnectKeepsRefreshAndChangesOnlyTheCaller(t *testing.T) {
	w := newPAC67World(t)
	alice := w.user("pac67-ms-callback-alice@example.com")
	bob := w.user("pac67-ms-callback-bob@example.com")
	w.providers(alice, "outlook", "meet") // her explicit choice in Settings
	w.providers(bob, "google", "meet")
	w.seedOutlook(alice, "ms-a1", "ms-kept-r", pac67Future)
	if _, err := storage.RecordCredentialLoss(w.db, alice, storage.CredentialOutlook); err != nil {
		t.Fatal(err)
	}
	w.d.ExchangeAnswer("ms-a2", "")

	ah := &authHandlers{db: w.db, jwtSecret: "secret", frontendURL: "http://app.test"}
	rec := httptest.NewRecorder()
	ah.microsoftCallback(rec, connectCallbackRequest(t, "secret", "/api/auth/microsoft/callback", msConnectCookie, connectProviderOutlook, alice, 0, alice, 0, "c1"))
	if rec.Code != http.StatusFound || !strings.HasSuffix(rec.Header().Get("Location"), "/auth/callback?provider=outlook&connect=credited") {
		t.Fatalf("callback = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	tok, _ := auth.LoadMicrosoftToken(w.db, alice)
	if tok.AccessToken != "ms-a2" || tok.RefreshToken != "ms-kept-r" {
		t.Errorf("stored = %+v, want ms-a2 with the kept refresh token", tok)
	}
	if w.lost(alice, storage.CredentialOutlook) {
		t.Error("the reconnect did not end the loss")
	}
	if s := w.status(alice); s["provider"] != "outlook" || s["connected"] != true {
		t.Errorf("alice's status = %v, want outlook connected", s)
	}
	if s := w.status(bob); s["provider"] != "google" {
		t.Errorf("AC-19: bob's calendar provider changed: %v", s)
	}
	if tok, _ := auth.LoadMicrosoftToken(w.db, bob); tok != nil {
		t.Errorf("AC-19: bob received a credential: %+v", tok)
	}
}

// AC-13, AC-16, AC-17, D-3, D-8, CONTRACT-9: Outlook disconnect.
func TestPAC67_DisconnectMicrosoft(t *testing.T) {
	w := newPAC67World(t)
	alice := w.user("pac67-dms-alice@example.com")
	bob := w.user("pac67-dms-bob@example.com")
	for _, u := range []uuid.UUID{alice, bob} {
		w.providers(u, "outlook", "teams")
		w.seedOutlook(u, "ms-"+u.String(), "msr-"+u.String(), pac67Future)
	}
	if _, err := storage.RecordCredentialLoss(w.db, alice, storage.CredentialOutlook); err != nil {
		t.Fatal(err)
	}

	assertBody(t, w.do(alice, http.MethodPost, "/api/auth/microsoft/disconnect", ""), http.StatusOK, `{"provider":"outlook","revocation":"not_offered"}`)
	if n := len(w.d.Calls("")); n != 0 {
		t.Errorf("AC-17: %d requests reached the provider, want none", n)
	}
	if tok, _ := auth.LoadMicrosoftToken(w.db, alice); tok != nil {
		t.Errorf("alice still holds a Microsoft credential: %+v", tok)
	}
	if s := w.status(alice); s["provider"] != "outlook" || s["connected"] != false {
		t.Errorf("alice's status = %v, want outlook (D-3) not connected", s)
	}
	w.assertRow(alice, "teams", false)
	s, _ := storage.GetOrCreateSettingsByUser(w.db, alice)
	if s.ConferencingProvider != "meet" || s.CalendarProvider != "outlook" {
		t.Errorf("alice's providers = %s/%s, want outlook/meet (D-3, D-8)", s.CalendarProvider, s.ConferencingProvider)
	}
	if w.lost(alice, storage.CredentialOutlook) {
		t.Error("the disconnect did not end the loss")
	}
	w.assertEntries(alice, `provider_disconnected {"provider":"outlook","revocation":"not_offered"}`)

	// B is untouched.
	if tok, _ := auth.LoadMicrosoftToken(w.db, bob); tok == nil || tok.RefreshToken != "msr-"+bob.String() {
		t.Error("bob's credential changed")
	}
	if s := w.status(bob); s["connected"] != true {
		t.Errorf("bob's status = %v", s)
	}
	w.assertRow(bob, "teams", true)
	w.assertEntries(bob)

	// Nothing stored: nothing_stored, no entry.
	assertBody(t, w.do(alice, http.MethodPost, "/api/auth/microsoft/disconnect", ""), http.StatusOK, `{"provider":"outlook","revocation":"nothing_stored"}`)
	w.assertEntries(alice, `provider_disconnected {"provider":"outlook","revocation":"not_offered"}`)

	// CONTRACT-9: an undecodable credential is removed, never a 500.
	carol := w.user("pac67-dms-carol@example.com")
	w.providers(carol, "google", "zoom")
	w.exec(`UPDATE settings SET microsoft_tokens = 'not json' WHERE user_id = $1`, carol)
	assertBody(t, w.do(carol, http.MethodPost, "/api/auth/microsoft/disconnect", ""), http.StatusOK, `{"provider":"outlook","revocation":"not_offered"}`)
	s, _ = storage.GetOrCreateSettingsByUser(w.db, carol)
	if s.MicrosoftTokens != "" || s.ConferencingProvider != "zoom" {
		t.Errorf("carol after disconnect: ms %q conferencing %q (want removed, zoom kept)", s.MicrosoftTokens, s.ConferencingProvider)
	}
	w.assertEntries(carol, `provider_disconnected {"provider":"outlook","revocation":"not_offered"}`)
}

// CONTRACT-9 (atomicity): a failure of the conferencing reset is the 500 and
// changes nothing.
func TestPAC67_DisconnectMicrosoftFailureChangesNothing(t *testing.T) {
	w := newPAC67World(t)
	dave := w.user("pac67-dms-atomic@example.com")
	w.providers(dave, "outlook", "teams")
	w.seedOutlook(dave, "ms-d", "msr-d", pac67Future)
	w.exec(fmt.Sprintf(`
		CREATE FUNCTION pac67_api_refuse_reset() RETURNS trigger AS $$
		BEGIN
			IF NEW.user_id = '%s' AND OLD.conferencing_provider = 'teams' AND NEW.conferencing_provider = 'meet' THEN
				RAISE EXCEPTION 'injected';
			END IF;
			RETURN NEW;
		END $$ LANGUAGE plpgsql`, dave))
	w.exec(`CREATE TRIGGER pac67_api_refuse_reset BEFORE UPDATE ON settings FOR EACH ROW EXECUTE FUNCTION pac67_api_refuse_reset()`)
	t.Cleanup(func() {
		_, _ = w.db.Exec(`DROP TRIGGER IF EXISTS pac67_api_refuse_reset ON settings`)
		_, _ = w.db.Exec(`DROP FUNCTION IF EXISTS pac67_api_refuse_reset()`)
	})
	assertBody(t, w.do(dave, http.MethodPost, "/api/auth/microsoft/disconnect", ""), http.StatusInternalServerError, `{"error":"internal error"}`)
	if tok, _ := auth.LoadMicrosoftToken(w.db, dave); tok == nil {
		t.Error("the credential was removed although the change failed")
	}
	w.assertEntries(dave)
}

// AC-14, AC-15, AC-16, D-2, D-9, CONTRACT-7, CONTRACT-8: Zoom disconnect.
func TestPAC67_DisconnectZoom(t *testing.T) {
	type outcome struct {
		body    string
		entries []string
	}
	confirmed := outcome{`{"provider":"zoom","revocation":"confirmed"}`, []string{`provider_disconnected {"provider":"zoom","revocation":"confirmed"}`}}
	unconfirmed := outcome{`{"provider":"zoom","revocation":"unconfirmed"}`, []string{
		`provider_disconnected {"provider":"zoom","revocation":"unconfirmed"}`,
		`revocation_unconfirmed {"provider":"zoom"}`,
	}}
	for _, tc := range []struct {
		name    string
		setup   func(w *pac67World, u uuid.UUID)
		want    outcome
		revoked string // the token the revocation must carry; "" means no revocation request
		renewed bool   // a renewal request precedes the revocation
	}{
		{"unexpired token, Zoom confirms", func(w *pac67World, u uuid.UUID) {
			w.seedZoom(u, "z-live", "z-r", &pac67Future)
		}, confirmed, "z-live", false},
		{"Zoom refuses the revocation", func(w *pac67World, u uuid.UUID) {
			w.seedZoom(u, "z-live", "z-r", &pac67Future)
			w.d.RevokeStatus(http.StatusBadRequest)
		}, unconfirmed, "z-live", false},
		{"Zoom unreachable", func(w *pac67World, u uuid.UUID) {
			w.seedZoom(u, "z-live", "z-r", &pac67Future)
			w.d.RevokeStatus(http.StatusServiceUnavailable)
		}, unconfirmed, "z-live", false},
		{"expired token is renewed in memory first", func(w *pac67World, u uuid.UUID) {
			w.seedZoom(u, "z-old", "z-r", &pac67Past)
		}, confirmed, "access-1", true},
		{"no stored expiry is renewed first", func(w *pac67World, u uuid.UUID) {
			if err := storage.SaveZoomTokens(w.db, u, "z-old", "z-r"); err != nil {
				w.t.Fatal(err)
			}
		}, confirmed, "access-1", true},
		{"renewal refused", func(w *pac67World, u uuid.UUID) {
			w.seedZoom(u, "z-old", "z-r", &pac67Past)
			w.d.Answer("z-r", oauthfake.InvalidGrant)
		}, unconfirmed, "", true},
		{"renewal fails temporarily", func(w *pac67World, u uuid.UUID) {
			w.seedZoom(u, "z-old", "z-r", &pac67Past)
			w.d.Answer("z-r", oauthfake.Unavailable)
		}, unconfirmed, "", true},
		{"loss already recorded", func(w *pac67World, u uuid.UUID) {
			w.seedZoom(u, "z-old", "z-r", &pac67Past)
			w.d.Answer("z-r", oauthfake.InvalidGrant)
			if _, err := storage.RecordCredentialLoss(w.db, u, storage.CredentialZoom); err != nil {
				w.t.Fatal(err)
			}
		}, unconfirmed, "", true},
		{"expired with no refresh token", func(w *pac67World, u uuid.UUID) {
			w.seedZoom(u, "z-old", "", &pac67Past)
		}, unconfirmed, "", false},
		{"undecodable credential", func(w *pac67World, u uuid.UUID) {
			w.exec(`INSERT INTO settings (user_id) VALUES ($1) ON CONFLICT (user_id) DO NOTHING`, u)
			w.exec(`UPDATE settings SET zoom_tokens = 'not json', conferencing_provider = 'zoom' WHERE user_id = $1`, u)
		}, unconfirmed, "", false},
		{"nothing stored", func(w *pac67World, u uuid.UUID) {
			w.providers(u, "google", "teams")
		}, outcome{`{"provider":"zoom","revocation":"nothing_stored"}`, nil}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newPAC67World(t)
			alice := w.user("pac67-dz-" + strings.ReplaceAll(tc.name, " ", "-") + "@example.com")
			bob := w.user("pac67-dz-bob-" + strings.ReplaceAll(tc.name, " ", "-") + "@example.com")
			w.seedZoom(bob, "bob-z", "bob-zr", &pac67Future)
			tc.setup(w, alice)

			assertBody(t, w.do(alice, http.MethodPost, "/api/conference/zoom/disconnect", ""), http.StatusOK, tc.want.body)

			renewals := w.d.Calls("/token")
			if tc.renewed != (len(renewals) == 1) || len(renewals) > 1 {
				t.Errorf("renewal requests = %+v, want renewed=%v", renewals, tc.renewed)
			}
			revokes := w.d.Calls("/revoke")
			switch {
			case tc.revoked == "" && len(revokes) != 0:
				t.Errorf("revocations = %+v, want none", revokes)
			case tc.revoked != "" && (len(revokes) != 1 || revokes[0].Form.Get("token") != tc.revoked || revokes[0].BasicUser != "zoom-client"):
				t.Errorf("revocations = %+v, want one carrying %s with client credentials", revokes, tc.revoked)
			}
			s, _ := storage.GetOrCreateSettingsByUser(w.db, alice)
			if s.ZoomTokens != "" || s.ConferencingProvider != "meet" {
				t.Errorf("after disconnect: tokens %q (renewed tokens must never be stored), conferencing %q", s.ZoomTokens, s.ConferencingProvider)
			}
			if w.lost(alice, storage.CredentialZoom) {
				t.Error("a disconnect left a loss recorded")
			}
			w.assertRow(alice, "zoom", false)
			w.assertEntries(alice, tc.want.entries...)
			w.assertRow(bob, "zoom", true)
			w.assertEntries(bob)
		})
	}
}

// CONTRACT-10: a failing activity insert never changes a response or a loss.
func TestPAC67_FailingAuditInsertChangesNoResponse(t *testing.T) {
	w := newPAC67World(t)
	erin := w.user("pac67-auditfail@example.com")
	w.exec(fmt.Sprintf(`
		CREATE FUNCTION pac67_refuse_audit() RETURNS trigger AS $$
		BEGIN
			IF NEW.user_id = '%s' THEN RAISE EXCEPTION 'injected audit failure'; END IF;
			RETURN NEW;
		END $$ LANGUAGE plpgsql`, erin))
	w.exec(`CREATE TRIGGER pac67_refuse_audit BEFORE INSERT ON audit_log FOR EACH ROW EXECUTE FUNCTION pac67_refuse_audit()`)
	t.Cleanup(func() {
		_, _ = w.db.Exec(`DROP TRIGGER IF EXISTS pac67_refuse_audit ON audit_log`)
		_, _ = w.db.Exec(`DROP FUNCTION IF EXISTS pac67_refuse_audit()`)
	})

	w.d.Answer("erin-dead", oauthfake.InvalidGrant)
	w.seedZoom(erin, "z-old", "erin-dead", &pac67Past)
	assertBody(t, w.do(erin, http.MethodPost, "/api/conference/create", pac67Meeting), http.StatusBadRequest, zoomLost)
	if !w.lost(erin, storage.CredentialZoom) {
		t.Error("the loss was not recorded")
	}
	w.assertRow(erin, "zoom", false)

	w.seedZoom(erin, "z-live", "z-r2", &pac67Future)
	assertBody(t, w.do(erin, http.MethodPost, "/api/conference/zoom/disconnect", ""), http.StatusOK, `{"provider":"zoom","revocation":"confirmed"}`)
	w.providers(erin, "outlook", "teams")
	w.seedOutlook(erin, "ms", "msr", pac67Future)
	assertBody(t, w.do(erin, http.MethodPost, "/api/auth/microsoft/disconnect", ""), http.StatusOK, `{"provider":"outlook","revocation":"not_offered"}`)
	w.assertEntries(erin)
}

// CONTRACT-11: a credential with no refresh token is never connected, and once
// expired answers the reconnect sentence without a request or an entry.
func TestPAC67_CredentialWithoutRefreshToken(t *testing.T) {
	w := newPAC67World(t)
	frank := w.user("pac67-norefresh@example.com")
	w.providers(frank, "outlook", "teams")
	w.seedOutlook(frank, "ms-only", "", pac67Future)
	w.seedZoom(frank, "zoom-only", "", &pac67Future)
	w.assertRow(frank, "teams", false)
	w.assertRow(frank, "zoom", false)
	if s := w.status(frank); s["connected"] != false {
		t.Errorf("outlook status = %v, want not connected", s)
	}
	w.seedOutlook(frank, "ms-only", "", pac67Past)
	w.providers(frank, "outlook", "teams")
	assertBody(t, w.do(frank, http.MethodPost, "/api/conference/create", pac67Meeting), http.StatusBadRequest, teamsLost)
	w.seedZoom(frank, "zoom-only", "", &pac67Past)
	assertBody(t, w.do(frank, http.MethodPost, "/api/conference/create", pac67Meeting), http.StatusBadRequest, zoomLost)
	if n := len(w.d.Calls("")); n != 0 {
		t.Errorf("requests = %d, want none", n)
	}
	w.assertEntries(frank)
	if w.lost(frank, storage.CredentialOutlook) || w.lost(frank, storage.CredentialZoom) {
		t.Error("a never-usable credential was recorded lost")
	}

	// An undecodable Zoom value is not connected either.
	w.exec(`UPDATE settings SET zoom_tokens = 'not json' WHERE user_id = $1`, frank)
	w.assertRow(frank, "zoom", false)
}

// API-052, CONTRACT-1, CONTRACT-5: the body is validated before any credential
// is read; the title test is ECMA-262 \s, not Go's.
func TestPAC67_CreateConferenceValidatesBody(t *testing.T) {
	w := newPAC67World(t)
	gina := w.user("pac67-validate@example.com")
	w.seedZoom(gina, "z-old", "z-r", &pac67Past)
	invalid := map[string]string{
		"not json":           `{`,
		"missing title":      `{"start":"2030-01-07T10:00:00Z","end":"2030-01-07T10:30:00Z"}`,
		"empty title":        `{"title":"","start":"2030-01-07T10:00:00Z","end":"2030-01-07T10:30:00Z"}`,
		"spaces":             `{"title":"   ","start":"2030-01-07T10:00:00Z","end":"2030-01-07T10:30:00Z"}`,
		"tab newline":        `{"title":"\t\n","start":"2030-01-07T10:00:00Z","end":"2030-01-07T10:30:00Z"}`,
		"nbsp":               `{"title":"\u00a0","start":"2030-01-07T10:00:00Z","end":"2030-01-07T10:30:00Z"}`,
		"em space":           `{"title":"\u2003","start":"2030-01-07T10:00:00Z","end":"2030-01-07T10:30:00Z"}`,
		"bom":                `{"title":"\ufeff","start":"2030-01-07T10:00:00Z","end":"2030-01-07T10:30:00Z"}`,
		"missing start":      `{"title":"T","end":"2030-01-07T10:30:00Z"}`,
		"missing end":        `{"title":"T","start":"2030-01-07T10:00:00Z"}`,
		"zero start":         `{"title":"T","start":"0001-01-01T00:00:00Z","end":"2030-01-07T10:30:00Z"}`,
		"end before start":   `{"title":"T","start":"2030-01-07T10:30:00Z","end":"2030-01-07T10:00:00Z"}`,
		"end equal to start": `{"title":"T","start":"2030-01-07T10:00:00Z","end":"2030-01-07T10:00:00Z"}`,
		"bad time":           `{"title":"T","start":"x","end":"2030-01-07T10:00:00Z"}`,
	}
	for name, body := range invalid {
		rec := w.do(gina, http.MethodPost, "/api/conference/create", body)
		if rec.Code != http.StatusBadRequest || strings.TrimSpace(rec.Body.String()) != `{"error":"invalid body"}` {
			t.Errorf("%s: %d %s, want 400 invalid body", name, rec.Code, rec.Body.String())
		}
	}
	if n := len(w.d.Calls("")); n != 0 {
		t.Errorf("an invalid body reached the provider (%d requests)", n)
	}
	// U+0085 is whitespace to Go but not to ECMA-262: accepted.
	rec := w.do(gina, http.MethodPost, "/api/conference/create", `{"title":"\u0085","start":"2030-01-07T10:00:00Z","end":"2030-01-07T10:30:00Z"}`)
	if rec.Code != http.StatusOK {
		t.Errorf("title U+0085: %d %s, want 200", rec.Code, rec.Body.String())
	}
}

// CONTRACT-2, U-12: the meet provider answers an empty joinUrl.
func TestPAC67_CreateConferenceMeet(t *testing.T) {
	w := newPAC67World(t)
	hal := w.user("pac67-meet@example.com")
	w.providers(hal, "google", "meet")
	rec := w.do(hal, http.MethodPost, "/api/conference/create", pac67Meeting)
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != http.StatusOK || len(got) != 2 || got["provider"] != "meet" || got["joinUrl"] != "" {
		t.Errorf("meet = %d %s, want 200 {provider: meet, joinUrl: \"\"} and nothing else", rec.Code, rec.Body.String())
	}
}

// The new operation is mounted behind requireAuth (contract: protected).
func TestPAC67_DisconnectMicrosoftIsProtectedAndMounted(t *testing.T) {
	db := openTestDB(t)
	r := chi.NewRouter()
	RegisterRoutes(r, db, nil, "secret", "http://app.test", "http://app.test")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/auth/microsoft/disconnect", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated = %d, want 401", rec.Code)
	}
	ivy := createTestUser(t, "pac67-mounted@example.com")
	req := httptest.NewRequest(http.MethodPost, "/api/auth/microsoft/disconnect", nil)
	req.AddCookie(pac35Session(t, "secret", ivy))
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	assertBody(t, rec, http.StatusOK, `{"provider":"outlook","revocation":"nothing_stored"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/conference/zoom/disconnect", nil)
	req.AddCookie(pac35Session(t, "secret", ivy))
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	assertBody(t, rec, http.StatusOK, `{"provider":"zoom","revocation":"nothing_stored"}`)
}

// CONTRACT-3: no activity entry written by PAC-67 carries token material;
// details hold only the server-chosen provider and revocation values.
func TestPAC67_EntriesCarryNoTokenMaterial(t *testing.T) {
	w := newPAC67World(t)
	jo := w.user("pac67-notokens@example.com")
	for _, rt := range []string{"SECRET-g-r", "SECRET-ms-r", "SECRET-z-r"} {
		w.d.Answer(rt, oauthfake.InvalidGrant)
	}
	w.providers(jo, "google", "teams")
	w.seedGoogle(jo, "SECRET-g-a", "SECRET-g-r", pac67Past)
	w.seedOutlook(jo, "SECRET-ms-a", "SECRET-ms-r", pac67Past)
	w.seedZoom(jo, "SECRET-z-a", "SECRET-z-r", &pac67Past)
	w.do(jo, http.MethodGet, "/api/calendar/events?start=2030-01-07T00:00:00Z&end=2030-01-08T00:00:00Z", "")
	w.do(jo, http.MethodPost, "/api/conference/create", pac67Meeting)
	w.providers(jo, "google", "zoom")
	w.do(jo, http.MethodPost, "/api/conference/create", pac67Meeting)
	w.do(jo, http.MethodPost, "/api/conference/zoom/disconnect", "")
	w.seedOutlook(jo, "SECRET-ms-a", "SECRET-ms-r", pac67Past)
	w.do(jo, http.MethodPost, "/api/auth/microsoft/disconnect", "")

	entries := w.entries(jo)
	kinds := map[string]bool{}
	for _, e := range entries {
		kinds[e.Action] = true
		if strings.Contains(e.Details, "SECRET") {
			t.Errorf("%s details carry token material: %s", e.Action, e.Details)
		}
		var m map[string]string
		if err := json.Unmarshal([]byte(e.Details), &m); err != nil {
			t.Errorf("%s details %q are not a JSON object of strings", e.Action, e.Details)
			continue
		}
		for k := range m {
			if k != "provider" && k != "revocation" {
				t.Errorf("%s details carry %q", e.Action, k)
			}
		}
	}
	for _, k := range []string{"connection_lost", "provider_disconnected", "revocation_unconfirmed"} {
		if !kinds[k] {
			t.Errorf("no %s entry was produced; entries = %+v", k, entries)
		}
	}
}
