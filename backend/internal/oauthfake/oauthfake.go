// Package oauthfake is a test double for an OAuth provider's token and
// revocation endpoints and for the bearer-authenticated API behind them
// (PAC-67). It stands in for Google, Microsoft and Zoom in unit and contract
// tests: tests point a credential's token URL at it and decide, per refresh
// token, whether a renewal succeeds, is permanently refused (invalid_grant) or
// fails temporarily (5xx). It never reaches a real provider.
package oauthfake

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
)

// Outcome is how the double answers a renewal with a given refresh token.
type Outcome int

const (
	// Renew issues a new access token (the default for any refresh token).
	Renew Outcome = iota
	// InvalidGrant is a permanent refusal: 400 {"error":"invalid_grant"}.
	InvalidGrant
	// Unavailable is a temporary failure: 503.
	Unavailable
)

// Call is one request the double received.
type Call struct {
	Path string
	Form url.Values
	// Bearer is the access token of an API call ("" for token endpoint calls).
	Bearer string
	// BasicUser is the client id of a Basic-authenticated call.
	BasicUser string
}

// Server is the double. Its zero configuration renews every refresh token
// without rotating it and answers every revocation with success.
type Server struct {
	*httptest.Server

	mu           sync.Mutex
	outcomes     map[string]Outcome
	rotate       bool
	rejectReused bool
	used         map[string]bool
	issued       int
	calls        []Call
	revokeStatus int
	apiStatus    int
	apiBody      string
	apiByPath    map[string][2]string
	exchange     map[string]string
}

// New starts a double. Close it with Server.Close.
func New() *Server {
	s := &Server{outcomes: map[string]Outcome{}, used: map[string]bool{}, revokeStatus: http.StatusOK, apiStatus: http.StatusOK, apiBody: `{}`}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

// TokenURL is the token endpoint; RevokeURL the revocation endpoint; APIBase
// the root of the bearer-authenticated API (any path below it is recorded).
func (s *Server) TokenURL() string  { return s.URL + "/token" }
func (s *Server) RevokeURL() string { return s.URL + "/revoke" }
func (s *Server) APIBase() string   { return s.URL + "/api" }

// Answer sets how renewals with refreshToken are answered.
func (s *Server) Answer(refreshToken string, o Outcome) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.outcomes[refreshToken] = o
}

// Rotate makes every successful renewal issue a new refresh token; with
// rejectReused, a refresh token that was already used is refused as
// invalid_grant, as Zoom and Microsoft do with rotated tokens.
func (s *Server) Rotate(rejectReused bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rotate, s.rejectReused = true, rejectReused
}

// RevokeStatus sets the status of every revocation answer (200 means success).
func (s *Server) RevokeStatus(code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revokeStatus = code
}

// ExchangeAnswer makes the authorization_code grant (a connect's code
// exchange) answer access with refresh; an empty refresh is omitted from the
// answer, as a provider that issues none would. Unset, the grant is refused.
func (s *Server) ExchangeAnswer(access, refresh string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.exchange = map[string]string{"access": access, "refresh": refresh}
}

// APIAnswer sets the status and JSON body of every API call.
func (s *Server) APIAnswer(code int, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.apiStatus, s.apiBody = code, body
}

// APIAnswerFor sets the answer of the API calls to one path (below APIBase,
// e.g. "/me/onlineMeetings"), overriding APIAnswer for it.
func (s *Server) APIAnswerFor(path string, code int, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.apiByPath == nil {
		s.apiByPath = map[string][2]string{}
	}
	s.apiByPath["/api"+path] = [2]string{fmt.Sprint(code), body}
}

// Calls returns the requests received on path ("/token", "/revoke", or an
// API path such as "/api/users/me/meetings"); "" returns every request.
func (s *Server) Calls(path string) []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Call, 0, len(s.calls))
	for _, c := range s.calls {
		if path == "" || c.Path == path {
			out = append(out, c)
		}
	}
	return out
}

// Saw reports whether any request carried token, as a refresh token, a
// revoked token or a bearer.
func (s *Server) Saw(token string) bool {
	for _, c := range s.Calls("") {
		if c.Bearer == token || c.Form.Get("refresh_token") == token || c.Form.Get("token") == token {
			return true
		}
	}
	return false
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	call := Call{Path: r.URL.Path, Form: r.PostForm}
	if u, _, ok := r.BasicAuth(); ok {
		call.BasicUser = u
	}
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		call.Bearer = strings.TrimPrefix(h, "Bearer ")
	}
	s.mu.Lock()
	s.calls = append(s.calls, call)
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.URL.Path == "/token":
		s.token(w, call.Form)
	case r.URL.Path == "/revoke":
		s.mu.Lock()
		code := s.revokeStatus
		s.mu.Unlock()
		w.WriteHeader(code)
		if code == http.StatusOK {
			_, _ = w.Write([]byte(`{"status":"success"}`))
		} else {
			_, _ = w.Write([]byte(`{"reason":"Invalid Token!","error":"invalid_request"}`))
		}
	case strings.HasPrefix(r.URL.Path, "/api/"):
		s.mu.Lock()
		code, body := s.apiStatus, s.apiBody
		if a, ok := s.apiByPath[r.URL.Path]; ok {
			_, _ = fmt.Sscan(a[0], &code)
			body = a[1]
		}
		s.mu.Unlock()
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (s *Server) token(w http.ResponseWriter, form url.Values) {
	if form.Get("grant_type") == "authorization_code" {
		s.mu.Lock()
		ex := s.exchange
		s.mu.Unlock()
		if ex != nil {
			resp := map[string]any{"access_token": ex["access"], "token_type": "Bearer", "expires_in": 3600}
			if ex["refresh"] != "" {
				resp["refresh_token"] = ex["refresh"]
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
	}
	if form.Get("grant_type") != "refresh_token" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"unsupported_grant_type"}`))
		return
	}
	rt := form.Get("refresh_token")
	s.mu.Lock()
	defer s.mu.Unlock()
	outcome := s.outcomes[rt]
	if s.rejectReused && s.used[rt] {
		outcome = InvalidGrant
	}
	switch outcome {
	case InvalidGrant:
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"refresh token revoked"}`))
		return
	case Unavailable:
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"temporarily_unavailable"}`))
		return
	}
	s.used[rt] = true
	s.issued++
	resp := map[string]any{
		"access_token": fmt.Sprintf("access-%d", s.issued),
		"token_type":   "Bearer",
		"expires_in":   3600,
	}
	if s.rotate {
		resp["refresh_token"] = fmt.Sprintf("refresh-%d", s.issued)
	}
	_ = json.NewEncoder(w).Encode(resp)
}
