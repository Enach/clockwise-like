package storage

import (
	"strings"
	"testing"
	"time"
)

// PAC-67: the stored-credential lifecycle — loss episodes, the Zoom record with
// its expiry, renewal write-back and the atomic local removal of a disconnect.

func TestCredentialLoss_OneEpisodePerOwnerAndProvider(t *testing.T) {
	db := openTestDB(t)
	alice := settingsTestUser(t, db, "pac67-loss-alice@example.com")
	bob := settingsTestUser(t, db, "pac67-loss-bob@example.com")

	started, err := RecordCredentialLoss(db, alice, CredentialZoom)
	if err != nil || !started {
		t.Fatalf("first refusal: started=%v err=%v, want a new loss", started, err)
	}
	if started, err := RecordCredentialLoss(db, alice, CredentialZoom); err != nil || started {
		t.Fatalf("second refusal in the same loss: started=%v err=%v, want no new loss", started, err)
	}
	if lost, _ := IsCredentialLost(db, alice, CredentialZoom); !lost {
		t.Error("alice's zoom is not reported lost")
	}
	for _, p := range []string{CredentialGoogle, CredentialOutlook} {
		if lost, _ := IsCredentialLost(db, alice, p); lost {
			t.Errorf("alice's %s reported lost by a zoom refusal", p)
		}
	}
	if lost, _ := IsCredentialLost(db, bob, CredentialZoom); lost {
		t.Error("bob's zoom reported lost by alice's refusal")
	}
	losses, err := CredentialLosses(db, alice)
	if err != nil || len(losses) != 1 || !losses[CredentialZoom] {
		t.Errorf("CredentialLosses(alice) = %v, %v", losses, err)
	}

	if err := ClearCredentialLoss(db, alice, CredentialZoom); err != nil {
		t.Fatal(err)
	}
	if lost, _ := IsCredentialLost(db, alice, CredentialZoom); lost {
		t.Error("loss not cleared")
	}
	if started, err := RecordCredentialLoss(db, alice, CredentialZoom); err != nil || !started {
		t.Fatalf("a refusal after the loss ended must start a new one: started=%v err=%v", started, err)
	}
	if _, err := RecordCredentialLoss(db, alice, "dropbox"); err == nil {
		t.Error("an unknown provider was accepted")
	}
}

func TestZoomCredential_StoresExpiryKeepsRefreshAndEndsLoss(t *testing.T) {
	db := openTestDB(t)
	alice := settingsTestUser(t, db, "pac67-zoomcred-alice@example.com")
	expiry := time.Date(2030, 1, 7, 10, 0, 0, 0, time.UTC)

	if err := SaveZoomCredential(db, alice, ZoomCredential{AccessToken: "a1", RefreshToken: "r1", Expiry: &expiry}); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordCredentialLoss(db, alice, CredentialZoom); err != nil {
		t.Fatal(err)
	}
	// A reconnect whose exchange returned no refresh token (AC-11).
	later := expiry.Add(time.Hour)
	if err := SaveZoomCredential(db, alice, ZoomCredential{AccessToken: "a2", Expiry: &later}); err != nil {
		t.Fatal(err)
	}
	s, _ := GetOrCreateSettingsByUser(db, alice)
	cred, err := DecodeZoomCredential(s.ZoomTokens)
	if err != nil {
		t.Fatal(err)
	}
	if cred.AccessToken != "a2" || cred.RefreshToken != "r1" || cred.Expiry == nil || !cred.Expiry.Equal(later) {
		t.Errorf("stored = %+v, want a2 / kept r1 / %v", cred, later)
	}
	if s.ConferencingProvider != "zoom" {
		t.Errorf("conferencing = %q, want zoom", s.ConferencingProvider)
	}
	if lost, _ := IsCredentialLost(db, alice, CredentialZoom); lost {
		t.Error("a reconnect did not end the loss")
	}
}

func TestDecodeZoomCredential(t *testing.T) {
	if _, err := DecodeZoomCredential("not json"); err == nil {
		t.Error("undecodable value accepted")
	}
	c, err := DecodeZoomCredential(`{"access_token":"a","refresh_token":"r"}`)
	if err != nil || c.AccessToken != "a" || c.RefreshToken != "r" || c.Expiry != nil {
		t.Errorf("pre-PAC-67 record = %+v, %v (no expiry expected)", c, err)
	}
}

func TestUpdateZoomCredential_IsOwnerOnlyAndNeverResurrects(t *testing.T) {
	db := openTestDB(t)
	alice := settingsTestUser(t, db, "pac67-zoomupd-alice@example.com")
	bob := settingsTestUser(t, db, "pac67-zoomupd-bob@example.com")
	if err := SaveZoomTokens(db, alice, "a1", "r1"); err != nil {
		t.Fatal(err)
	}
	if err := SaveZoomTokens(db, bob, "b1", "rb"); err != nil {
		t.Fatal(err)
	}
	exp := time.Date(2031, 2, 3, 4, 5, 6, 0, time.UTC)
	if err := UpdateZoomCredential(db, alice, ZoomCredential{AccessToken: "a2", RefreshToken: "r2", Expiry: &exp}); err != nil {
		t.Fatal(err)
	}
	a, _ := GetOrCreateSettingsByUser(db, alice)
	b, _ := GetOrCreateSettingsByUser(db, bob)
	if !strings.Contains(a.ZoomTokens, `"a2"`) || !strings.Contains(a.ZoomTokens, `"r2"`) {
		t.Errorf("alice's renewal not stored: %s", a.ZoomTokens)
	}
	if b.ZoomTokens != `{"access_token":"b1","refresh_token":"rb"}` {
		t.Errorf("bob's credential changed: %s", b.ZoomTokens)
	}

	if _, err := RemoveZoomCredential(db, alice); err != nil {
		t.Fatal(err)
	}
	if err := UpdateZoomCredential(db, alice, ZoomCredential{AccessToken: "late", RefreshToken: "late"}); err != nil {
		t.Fatal(err)
	}
	a, _ = GetOrCreateSettingsByUser(db, alice)
	if a.ZoomTokens != "" {
		t.Errorf("a renewal finishing after a disconnect re-stored the credential: %s", a.ZoomTokens)
	}
}

func TestRemoveZoomCredential_IsAtomicAndCallerScoped(t *testing.T) {
	db := openTestDB(t)
	alice := settingsTestUser(t, db, "pac67-zoomrm-alice@example.com")
	bob := settingsTestUser(t, db, "pac67-zoomrm-bob@example.com")
	if _, err := db.Exec(`INSERT INTO settings (user_id) VALUES ($1) ON CONFLICT DO NOTHING`, alice); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE settings SET zoom_tokens = 'not json', conferencing_provider = 'zoom' WHERE user_id = $1`, alice); err != nil {
		t.Fatal(err)
	}
	if err := SaveZoomTokens(db, bob, "b1", "rb"); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordCredentialLoss(db, alice, CredentialZoom); err != nil {
		t.Fatal(err)
	}

	raw, err := RemoveZoomCredential(db, alice)
	if err != nil || raw != "not json" {
		t.Fatalf("RemoveZoomCredential = %q, %v; want the removed value", raw, err)
	}
	a, _ := GetOrCreateSettingsByUser(db, alice)
	if a.ZoomTokens != "" || a.ConferencingProvider != "meet" {
		t.Errorf("alice after removal: tokens %q, conferencing %q", a.ZoomTokens, a.ConferencingProvider)
	}
	if lost, _ := IsCredentialLost(db, alice, CredentialZoom); lost {
		t.Error("removal did not end the loss")
	}
	b, _ := GetOrCreateSettingsByUser(db, bob)
	if b.ZoomTokens == "" || b.ConferencingProvider != "zoom" {
		t.Error("removing alice's zoom touched bob's")
	}

	raw, err = RemoveZoomCredential(db, alice)
	if err != nil || raw != "" {
		t.Errorf("second removal = %q, %v; want nothing stored", raw, err)
	}
	carol := settingsTestUser(t, db, "pac67-zoomrm-carol@example.com")
	if raw, err := RemoveZoomCredential(db, carol); err != nil || raw != "" {
		t.Errorf("removal for a user with no settings row = %q, %v", raw, err)
	}
}

func TestRemoveMicrosoftCredential_AtomicWithTheConferencingReset(t *testing.T) {
	db := openTestDB(t)
	alice := settingsTestUser(t, db, "pac67-msrm-alice@example.com")
	bob := settingsTestUser(t, db, "pac67-msrm-bob@example.com")
	for _, u := range []struct {
		id           any
		ms, conf, cp string
	}{
		{alice, "not json", "teams", "outlook"},
		{bob, `{"access_token":"b","refresh_token":"rb"}`, "teams", "outlook"},
	} {
		if _, err := db.Exec(`INSERT INTO settings (user_id) VALUES ($1) ON CONFLICT DO NOTHING`, u.id); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE settings SET microsoft_tokens = $2, conferencing_provider = $3, calendar_provider = $4 WHERE user_id = $1`, u.id, u.ms, u.conf, u.cp); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := RecordCredentialLoss(db, alice, CredentialOutlook); err != nil {
		t.Fatal(err)
	}

	raw, err := RemoveMicrosoftCredential(db, alice)
	if err != nil || raw != "not json" {
		t.Fatalf("RemoveMicrosoftCredential = %q, %v", raw, err)
	}
	a, _ := GetOrCreateSettingsByUser(db, alice)
	if a.MicrosoftTokens != "" || a.ConferencingProvider != "meet" || a.CalendarProvider != "outlook" {
		t.Errorf("alice after removal: ms %q conf %q cal %q (want '', meet, outlook: D-3, D-8)", a.MicrosoftTokens, a.ConferencingProvider, a.CalendarProvider)
	}
	if lost, _ := IsCredentialLost(db, alice, CredentialOutlook); lost {
		t.Error("removal did not end the loss")
	}
	b, _ := GetOrCreateSettingsByUser(db, bob)
	if b.MicrosoftTokens == "" || b.ConferencingProvider != "teams" {
		t.Error("removing alice's credential touched bob's")
	}

	// Conferencing other than teams is left alone.
	if _, err := db.Exec(`UPDATE settings SET microsoft_tokens = '{"access_token":"x"}', conferencing_provider = 'zoom' WHERE user_id = $1`, alice); err != nil {
		t.Fatal(err)
	}
	if _, err := RemoveMicrosoftCredential(db, alice); err != nil {
		t.Fatal(err)
	}
	if a, _ = GetOrCreateSettingsByUser(db, alice); a.ConferencingProvider != "zoom" {
		t.Errorf("conferencing %q, want zoom kept", a.ConferencingProvider)
	}

	// An injected failure of the conferencing reset leaves everything as it was.
	if _, err := db.Exec(`
		CREATE FUNCTION pac67_refuse_reset() RETURNS trigger AS $$
		BEGIN
			IF OLD.conferencing_provider = 'teams' AND NEW.conferencing_provider = 'meet' THEN
				RAISE EXCEPTION 'injected conferencing reset failure';
			END IF;
			RETURN NEW;
		END $$ LANGUAGE plpgsql;
		CREATE TRIGGER pac67_refuse_reset BEFORE UPDATE ON settings
			FOR EACH ROW EXECUTE FUNCTION pac67_refuse_reset();`); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordCredentialLoss(db, bob, CredentialOutlook); err != nil {
		t.Fatal(err)
	}
	if _, err := RemoveMicrosoftCredential(db, bob); err == nil {
		t.Fatal("the injected failure was not reported")
	}
	b, _ = GetOrCreateSettingsByUser(db, bob)
	if b.MicrosoftTokens == "" || b.ConferencingProvider != "teams" {
		t.Errorf("a failed removal changed bob's row: ms %q conf %q", b.MicrosoftTokens, b.ConferencingProvider)
	}
	if lost, _ := IsCredentialLost(db, bob, CredentialOutlook); !lost {
		t.Error("a failed removal cleared bob's loss")
	}
}

func TestMicrosoftCredential_UpdateIsOwnerOnlyAndNeverResurrects(t *testing.T) {
	db := openTestDB(t)
	alice := settingsTestUser(t, db, "pac67-msupd-alice@example.com")
	bob := settingsTestUser(t, db, "pac67-msupd-bob@example.com")
	for _, u := range []any{alice, bob} {
		if _, err := db.Exec(`INSERT INTO settings (user_id, microsoft_tokens) VALUES ($1, '{"access_token":"old"}') ON CONFLICT (user_id) DO UPDATE SET microsoft_tokens = EXCLUDED.microsoft_tokens`, u); err != nil {
			t.Fatal(err)
		}
	}
	if err := UpdateMicrosoftCredential(db, alice, `{"access_token":"new"}`); err != nil {
		t.Fatal(err)
	}
	a, _ := GetOrCreateSettingsByUser(db, alice)
	b, _ := GetOrCreateSettingsByUser(db, bob)
	if a.MicrosoftTokens != `{"access_token":"new"}` || b.MicrosoftTokens != `{"access_token":"old"}` {
		t.Errorf("alice %q bob %q", a.MicrosoftTokens, b.MicrosoftTokens)
	}
	if _, err := RemoveMicrosoftCredential(db, alice); err != nil {
		t.Fatal(err)
	}
	if err := UpdateMicrosoftCredential(db, alice, `{"access_token":"late"}`); err != nil {
		t.Fatal(err)
	}
	if a, _ = GetOrCreateSettingsByUser(db, alice); a.MicrosoftTokens != "" {
		t.Errorf("a late renewal re-stored a removed credential: %q", a.MicrosoftTokens)
	}
}

// Migration 028 up, down, up: the down step drops only the derived loss
// markers; no credential is touched (spec section 7).
func TestMigration028_CredentialLosses_UpDownUp(t *testing.T) {
	db, to := migrationTestDB(t)
	to(28)
	var user string
	if err := db.QueryRow(`INSERT INTO users (email) VALUES ('pac67-mig@example.com') RETURNING id`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO settings (user_id, microsoft_tokens, zoom_tokens) VALUES ($1, '{"access_token":"m"}', '{"access_token":"z"}')`, user); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO credential_losses (user_id, provider) VALUES ($1, 'zoom')`, user); err != nil {
		t.Fatalf("028 up: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO credential_losses (user_id, provider) VALUES ($1, 'teams')`, user); err == nil {
		t.Error("028 accepted a provider outside google/outlook/zoom")
	}
	to(27)
	var exists bool
	if err := db.QueryRow(`SELECT to_regclass('credential_losses') IS NOT NULL`).Scan(&exists); err != nil || exists {
		t.Fatalf("028 down left the table (exists=%v, err=%v)", exists, err)
	}
	var ms, zoom string
	if err := db.QueryRow(`SELECT microsoft_tokens, zoom_tokens FROM settings WHERE user_id = $1`, user).Scan(&ms, &zoom); err != nil || ms == "" || zoom == "" {
		t.Errorf("028 down touched a credential: ms %q zoom %q err %v", ms, zoom, err)
	}
	to(28)
	if _, err := db.Exec(`INSERT INTO credential_losses (user_id, provider) VALUES ($1, 'google')`, user); err != nil {
		t.Errorf("028 up again: %v", err)
	}
}
