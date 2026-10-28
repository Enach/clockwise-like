package oauthfake

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func send(t *testing.T, method, target, contentType, body, bearer string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, target, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func post(t *testing.T, target string, form url.Values) (int, map[string]any) {
	t.Helper()
	resp := send(t, http.MethodPost, target, "application/x-www-form-urlencoded", form.Encode(), "")
	defer resp.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

func TestDouble_RenewRefuseRotateAndRevoke(t *testing.T) {
	s := New()
	defer s.Close()
	refresh := func(rt string) (int, map[string]any) {
		return post(t, s.TokenURL(), url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt}})
	}

	if code, body := refresh("r1"); code != 200 || body["access_token"] != "access-1" || body["refresh_token"] != nil {
		t.Errorf("plain renewal = %d %v", code, body)
	}
	s.Answer("dead", InvalidGrant)
	if code, body := refresh("dead"); code != 400 || body["error"] != "invalid_grant" {
		t.Errorf("refused renewal = %d %v", code, body)
	}
	s.Answer("flaky", Unavailable)
	if code, _ := refresh("flaky"); code != 503 {
		t.Errorf("temporary failure = %d", code)
	}
	if code, _ := post(t, s.TokenURL(), url.Values{"grant_type": {"authorization_code"}}); code != 400 {
		t.Errorf("non-refresh grant = %d", code)
	}
	s.ExchangeAnswer("ex-access", "")
	if code, body := post(t, s.TokenURL(), url.Values{"grant_type": {"authorization_code"}, "code": {"c"}}); code != 200 || body["access_token"] != "ex-access" || body["refresh_token"] != nil {
		t.Errorf("exchange without refresh = %d %v", code, body)
	}
	s.ExchangeAnswer("ex-access", "ex-refresh")
	if _, body := post(t, s.TokenURL(), url.Values{"grant_type": {"authorization_code"}, "code": {"c"}}); body["refresh_token"] != "ex-refresh" {
		t.Errorf("exchange with refresh = %v", body)
	}

	s.Rotate(true)
	_, body := refresh("r2")
	next, _ := body["refresh_token"].(string)
	if next == "" {
		t.Fatalf("rotation issued no refresh token: %v", body)
	}
	if code, body := refresh("r2"); code != 400 || body["error"] != "invalid_grant" {
		t.Errorf("a reused rotated token = %d %v, want invalid_grant", code, body)
	}
	if code, _ := refresh(next); code != 200 {
		t.Errorf("the rotated token = %d", code)
	}
	if got := len(s.Calls("/token")); got != 9 {
		t.Errorf("token calls = %d, want 9", got)
	}
	if !s.Saw("r2") || s.Saw("never-sent") {
		t.Error("Saw reports the wrong tokens")
	}

	if code, body := post(t, s.RevokeURL(), url.Values{"token": {"a"}}); code != 200 || body["status"] != "success" {
		t.Errorf("revocation = %d %v", code, body)
	}
	s.RevokeStatus(http.StatusBadRequest)
	if code, _ := post(t, s.RevokeURL(), url.Values{"token": {"a"}}); code != 400 {
		t.Errorf("refused revocation = %d", code)
	}

	s.APIAnswer(201, `{"id":"m1"}`)
	resp := send(t, http.MethodPost, s.APIBase()+"/me/onlineMeetings", "", "", "tok-1")
	resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Errorf("api status = %d", resp.StatusCode)
	}
	if calls := s.Calls("/api/me/onlineMeetings"); len(calls) != 1 || calls[0].Bearer != "tok-1" {
		t.Errorf("api calls = %+v", calls)
	}
	s.APIAnswerFor("/users/me/meetings", 202, `{"id":1}`)
	resp = send(t, http.MethodPost, s.APIBase()+"/users/me/meetings", "application/json", "", "")
	resp.Body.Close()
	if resp.StatusCode != 202 {
		t.Errorf("path-specific answer = %d, want 202", resp.StatusCode)
	}
	resp = send(t, http.MethodGet, s.URL+"/elsewhere", "", "", "")
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("unknown path answered %d", resp.StatusCode)
	}
}
