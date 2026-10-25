package main

// PAC-50 AC-23: the identity-provider half of the Google stand-in, enough for
// the SSO admin step-up (backend/auth/stepup.go) to run end to end. The
// backend reaches it through PACEDAY_GOOGLE_IDP_BASE_URL under the PAC-43
// test-mode declaration; the test plays the browser and reaches /o/oauth2/v2/auth
// on the host port.
//
// It asserts the account the step-up asked for (login_hint), with `hd` set to
// that address's domain, and reports every account as a Workspace
// administrator. It is a test fixture: no consent screen, no secrets checked.

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

type idpGrant struct {
	email, nonce, clientID string
}

type idp struct {
	key    *rsa.PrivateKey
	issuer string

	mu     sync.Mutex
	codes  map[string]idpGrant
	tokens map[string]string // access token -> email
}

func newIDP() *idp {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		log.Fatalf("fake idp key: %v", err)
	}
	issuer := os.Getenv("FAKE_IDP_ISSUER")
	if issuer == "" {
		issuer = "http://fake-google:8080"
	}
	return &idp{key: key, issuer: issuer, codes: map[string]idpGrant{}, tokens: map[string]string{}}
}

func (s *Server) registerIdP() {
	p := newIDP()
	s.mux.HandleFunc("/o/oauth2/v2/auth", p.authorize)
	s.mux.HandleFunc("/token", p.token)
	s.mux.HandleFunc("/oauth2/v3/certs", p.certs)
	s.mux.HandleFunc("/admin/directory/v1/users/", p.directoryUser)
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// authorize signs the login_hint account in at once and sends the browser
// back to redirect_uri with a code and the state.
func (p *idp) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	redirect, err := url.Parse(q.Get("redirect_uri"))
	email := strings.ToLower(q.Get("login_hint"))
	if err != nil || redirect.Scheme == "" || q.Get("state") == "" || !strings.Contains(email, "@") || q.Get("response_type") != "code" {
		http.Error(w, "fake idp: redirect_uri, state, login_hint and response_type=code are required", http.StatusBadRequest)
		return
	}
	code := randomHex(16)
	p.mu.Lock()
	p.codes[code] = idpGrant{email: email, nonce: q.Get("nonce"), clientID: q.Get("client_id")}
	p.mu.Unlock()
	v := redirect.Query()
	v.Set("code", code)
	v.Set("state", q.Get("state"))
	redirect.RawQuery = v.Encode()
	http.Redirect(w, r, redirect.String(), http.StatusFound)
}

func (p *idp) token(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.ParseForm() != nil {
		http.Error(w, `{"error":"invalid_request"}`, http.StatusBadRequest)
		return
	}
	code := r.PostForm.Get("code")
	p.mu.Lock()
	g, ok := p.codes[code]
	delete(p.codes, code) // single use
	at := "fake-idp-at-" + randomHex(12)
	if ok {
		p.tokens[at] = g.email
	}
	p.mu.Unlock()
	if !ok {
		http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
		return
	}
	now := time.Now()
	claims := map[string]any{
		"iss": p.issuer, "aud": g.clientID, "sub": "fake-" + g.email,
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
		"email": g.email, "email_verified": true, "hd": g.email[strings.LastIndex(g.email, "@")+1:],
		"nonce": g.nonce,
	}
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "fake-idp"})
	body, _ := json.Marshal(claims)
	in := b64url(header) + "." + b64url(body)
	sum := sha256.Sum256([]byte(in))
	sig, err := rsa.SignPKCS1v15(rand.Reader, p.key, crypto.SHA256, sum[:])
	if err != nil {
		http.Error(w, `{"error":"server_error"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token": at, "token_type": "Bearer", "expires_in": 3600, "id_token": in + "." + b64url(sig),
	})
}

func (p *idp) certs(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
		"kty": "RSA", "alg": "RS256", "use": "sig", "kid": "fake-idp",
		"n": b64url(p.key.N.Bytes()), "e": b64url(big.NewInt(int64(p.key.E)).Bytes()),
	}}})
}

// directoryUser answers the Directory API's users.get for the account the
// access token belongs to, and only that one.
func (p *idp) directoryUser(w http.ResponseWriter, r *http.Request) {
	at := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	userKey, _ := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/admin/directory/v1/users/"))
	p.mu.Lock()
	email, ok := p.tokens[at]
	p.mu.Unlock()
	if !ok || !strings.EqualFold(email, userKey) {
		http.Error(w, `{"error":{"code":403,"message":"Not Authorized to access this resource/api"}}`, http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"primaryEmail": email, "isAdmin": true, "isDelegatedAdmin": false})
}
