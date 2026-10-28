package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// The stand-in's step-up surface: authorize -> code -> token -> directory.
func TestIdP_RoundTrip(t *testing.T) {
	srv := httptest.NewServer(NewServer(NewStore()))
	defer srv.Close()
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	auth := srv.URL + "/o/oauth2/v2/auth?" + url.Values{
		"redirect_uri": {"http://localhost:8088/api/admin/sso/stepup/callback"}, "state": {"s1"},
		"nonce": {"n1"}, "login_hint": {"Admin@Acme.test"}, "client_id": {"cid"}, "response_type": {"code"},
	}.Encode()
	resp, err := noFollow.Get(auth)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if resp.StatusCode != http.StatusFound || loc.Query().Get("state") != "s1" || loc.Query().Get("code") == "" {
		t.Fatalf("authorize = %d %s", resp.StatusCode, loc)
	}

	resp, err = http.PostForm(srv.URL+"/token", url.Values{"code": {loc.Query().Get("code")}, "grant_type": {"authorization_code"}})
	if err != nil {
		t.Fatal(err)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		IDToken     string `json:"id_token"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&tok)
	resp.Body.Close()
	parts := strings.Split(tok.IDToken, ".")
	if len(parts) != 3 {
		t.Fatalf("id_token = %q", tok.IDToken)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var claims map[string]any
	_ = json.Unmarshal(raw, &claims)
	if claims["email"] != "admin@acme.test" || claims["hd"] != "acme.test" || claims["nonce"] != "n1" || claims["aud"] != "cid" {
		t.Fatalf("claims = %v", claims)
	}

	// The code is single use.
	resp, _ = http.PostForm(srv.URL+"/token", url.Values{"code": {loc.Query().Get("code")}})
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("second exchange = %d", resp.StatusCode)
	}

	for _, c := range []struct {
		user, token string
		want        int
	}{
		{"admin@acme.test", tok.AccessToken, 200},
		{"other@acme.test", tok.AccessToken, 403},
		{"admin@acme.test", "nope", 403},
	} {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/admin/directory/v1/users/"+url.PathEscape(c.user), nil)
		req.Header.Set("Authorization", "Bearer "+c.token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != c.want {
			t.Errorf("directory %s = %d, want %d", c.user, resp.StatusCode, c.want)
		}
	}
}
