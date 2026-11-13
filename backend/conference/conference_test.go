package conference

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/internal/oauthfake"
	"github.com/Enach/paceday/backend/storage"
	"golang.org/x/oauth2"
)

// PAC-67: providers take their access token from a renewing token source, Zoom
// revocation, and the provider factory's refusals.

type failingSource struct{ err error }

func (f failingSource) Token() (*oauth2.Token, error) { return nil, f.err }

func withAPIBase(t *testing.T, d *oauthfake.Server) {
	t.Helper()
	oldZoom, oldGraph := ZoomAPIBase, GraphBase
	ZoomAPIBase, GraphBase = d.APIBase(), d.APIBase()
	t.Cleanup(func() { ZoomAPIBase, GraphBase = oldZoom, oldGraph })
}

func TestProviders_UseTheTokenSourcesAccessToken(t *testing.T) {
	d := oauthfake.New()
	defer d.Close()
	withAPIBase(t, d)
	start := time.Date(2030, 1, 7, 10, 0, 0, 0, time.UTC)

	d.APIAnswer(http.StatusCreated, `{"id":123,"join_url":"https://zoom.example/j/123","password":"pw"}`)
	z := &ZoomProvider{Tokens: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "zoom-renewed"})}
	got, err := z.CreateMeeting(context.Background(), "Sync", start, start.Add(30*time.Minute))
	if err != nil || got.Provider != "zoom" || got.JoinURL != "https://zoom.example/j/123" || got.MeetingID != "123" || got.Password != "pw" {
		t.Fatalf("zoom = %+v, %v", got, err)
	}
	if c := d.Calls("/api/users/me/meetings"); len(c) != 1 || c[0].Bearer != "zoom-renewed" {
		t.Errorf("zoom calls = %+v, want one bearer zoom-renewed", c)
	}

	d.APIAnswer(http.StatusCreated, `{"id":"t1","joinWebUrl":"https://teams.example/t1"}`)
	tm := &TeamsProvider{Tokens: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "ms-renewed"})}
	got, err = tm.CreateMeeting(context.Background(), "Sync", start, start.Add(time.Hour))
	if err != nil || got.Provider != "teams" || got.JoinURL != "https://teams.example/t1" || got.MeetingID != "t1" {
		t.Fatalf("teams = %+v, %v", got, err)
	}
	if c := d.Calls("/api/me/onlineMeetings"); len(c) != 1 || c[0].Bearer != "ms-renewed" {
		t.Errorf("teams calls = %+v, want one bearer ms-renewed", c)
	}

	// Without a source the stored access token is used as before.
	if _, err := NewTeamsProvider("legacy").CreateMeeting(context.Background(), "x", start, start.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if c := d.Calls("/api/me/onlineMeetings"); c[len(c)-1].Bearer != "legacy" {
		t.Errorf("legacy bearer = %q", c[len(c)-1].Bearer)
	}

	// A provider error is an error.
	d.APIAnswer(http.StatusInternalServerError, `{}`)
	if _, err := z.CreateMeeting(context.Background(), "x", start, start.Add(time.Hour)); err == nil {
		t.Error("zoom 500 was not an error")
	}
	if _, err := tm.CreateMeeting(context.Background(), "x", start, start.Add(time.Hour)); err == nil {
		t.Error("teams 500 was not an error")
	}
}

func TestProviders_ATokenFailureSendsNothing(t *testing.T) {
	d := oauthfake.New()
	defer d.Close()
	withAPIBase(t, d)
	lost := errors.New("connection lost")
	start := time.Now()
	for name, p := range map[string]Provider{
		"zoom":  &ZoomProvider{Tokens: failingSource{lost}},
		"teams": &TeamsProvider{Tokens: failingSource{lost}},
	} {
		if _, err := p.CreateMeeting(context.Background(), "x", start, start.Add(time.Hour)); !errors.Is(err, lost) {
			t.Errorf("%s: err = %v, want the token source's error", name, err)
		}
	}
	if n := len(d.Calls("")); n != 0 {
		t.Errorf("requests = %d, want none", n)
	}
}

func TestMeetProvider_AnswersAnEmptyJoinURL(t *testing.T) {
	got, err := (&MeetProvider{}).CreateMeeting(context.Background(), "x", time.Now(), time.Now())
	if err != nil || got.Provider != "meet" || got.JoinURL != "" || got.MeetingID != "" {
		t.Errorf("meet = %+v, %v (U-12: provider meet, joinUrl empty)", got, err)
	}
}

func TestNewProvider_ZoomCredentialStates(t *testing.T) {
	for name, tc := range map[string]struct {
		s    storage.Settings
		want error
	}{
		"blank":       {storage.Settings{ConferencingProvider: "zoom"}, ErrZoomNotConnected},
		"undecodable": {storage.Settings{ConferencingProvider: "zoom", ZoomTokens: "not json"}, ErrZoomInvalidCredential},
	} {
		if _, err := NewProvider(&tc.s); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", name, err, tc.want)
		}
	}
	p, err := NewProvider(&storage.Settings{ConferencingProvider: "zoom", ZoomTokens: `{"access_token":"a","refresh_token":"r"}`})
	if z, ok := p.(*ZoomProvider); err != nil || !ok || z.AccessToken != "a" {
		t.Errorf("decodable zoom = %#v, %v", p, err)
	}
	if p, err := NewProvider(&storage.Settings{ConferencingProvider: "meet"}); err != nil {
		t.Errorf("meet: %v", err)
	} else if _, ok := p.(*MeetProvider); !ok {
		t.Errorf("meet = %#v", p)
	}
	if _, err := NewProvider(&storage.Settings{ConferencingProvider: "teams"}); err == nil {
		t.Error("teams through NewProvider did not refuse")
	}
}

func TestRevokeZoomToken(t *testing.T) {
	d := oauthfake.New()
	defer d.Close()
	old := ZoomRevokeURL
	ZoomRevokeURL = d.RevokeURL()
	defer func() { ZoomRevokeURL = old }()

	if err := RevokeZoomToken(context.Background(), "cid", "secret", "access-1"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	c := d.Calls("/revoke")
	if len(c) != 1 || c[0].Form.Get("token") != "access-1" || c[0].BasicUser != "cid" {
		t.Errorf("revocation request = %+v, want token=access-1 with Basic client credentials", c)
	}
	d.RevokeStatus(http.StatusBadRequest)
	if err := RevokeZoomToken(context.Background(), "cid", "secret", "access-1"); err == nil {
		t.Error("a refused revocation was reported confirmed")
	}
	d.RevokeStatus(http.StatusOK)
	d.Close()
	if err := RevokeZoomToken(context.Background(), "cid", "secret", "access-1"); err == nil {
		t.Error("an unreachable Zoom was reported confirmed")
	}
}

func TestRevokeZoomToken_SuccessMeansStatusSuccess(t *testing.T) {
	d := oauthfake.New()
	defer d.Close()
	d.APIAnswer(http.StatusOK, `{"status":"nope"}`)
	old := ZoomRevokeURL
	ZoomRevokeURL = d.APIBase() + "/revoke"
	defer func() { ZoomRevokeURL = old }()
	if err := RevokeZoomToken(context.Background(), "cid", "secret", "a"); err == nil {
		t.Error(`a 200 without {"status":"success"} was reported confirmed`)
	}
}

func TestZoomOAuthConfig(t *testing.T) {
	t.Setenv("ZOOM_CLIENT_ID", "")
	if ZoomOAuthConfigFromEnv() != nil {
		t.Error("an unconfigured server has a Zoom configuration")
	}
	t.Setenv("ZOOM_CLIENT_ID", "cid")
	t.Setenv("ZOOM_CLIENT_SECRET", "sec")
	c := ZoomOAuthConfigFromEnv()
	if c == nil || c.ClientID != "cid" || c.ClientSecret != "sec" || c.Endpoint.TokenURL != ZoomTokenURL || c.Endpoint.AuthStyle != oauth2.AuthStyleInHeader {
		t.Errorf("config = %+v", c)
	}
}

func TestZoomExchangeCode_UsesTheTokenEndpoint(t *testing.T) {
	d := oauthfake.New()
	defer d.Close()
	old := ZoomTokenURL
	ZoomTokenURL = d.TokenURL()
	defer func() { ZoomTokenURL = old }()
	// The double only implements the refresh grant; an exchange is refused,
	// which is enough to show where it was sent.
	if _, err := ZoomExchangeCode(context.Background(), "cid", "sec", "https://app/cb", "code-1"); err == nil {
		t.Error("expected the double to refuse an authorization_code grant")
	}
	if c := d.Calls("/token"); len(c) != 1 || c[0].Form.Get("code") != "code-1" || c[0].BasicUser != "cid" {
		t.Errorf("exchange request = %+v", c)
	}
	if u := ZoomAuthURL("cid", "https://app/cb", "st"); u == "" {
		t.Error("no auth URL")
	}
}
