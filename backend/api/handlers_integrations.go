package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Enach/paceday/backend/storage"
)

type integrationsHandlers struct {
	db *sql.DB
}

// ── state helpers ─────────────────────────────────────────────────────────────

func randomState() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)
}

// ── Slack OAuth ───────────────────────────────────────────────────────────────

func (h *integrationsHandlers) slackConnect(w http.ResponseWriter, r *http.Request) {
	clientID := os.Getenv("SLACK_CLIENT_ID")
	redirectURI := os.Getenv("SLACK_REDIRECT_URI")
	if clientID == "" {
		http.Error(w, "Slack not configured", http.StatusServiceUnavailable)
		return
	}
	state := randomState()
	http.SetCookie(w, &http.Cookie{Name: "slack_oauth_state", Value: state, Path: "/", MaxAge: 300, HttpOnly: true})

	q := url.Values{}
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("scope", "search:read,users:read,channels:read,chat:write")
	q.Set("user_scope", "search:read")
	q.Set("state", state)
	http.Redirect(w, r, "https://slack.com/oauth/v2/authorize?"+q.Encode(), http.StatusFound)
}

func (h *integrationsHandlers) slackCallback(w http.ResponseWriter, r *http.Request) {
	stateCookie, err := r.Cookie("slack_oauth_state")
	if err != nil || stateCookie.Value != r.URL.Query().Get("state") {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}
	userID := userIDFromCtx(r.Context())

	code := r.URL.Query().Get("code")
	tok, err := slackExchangeCode(code,
		os.Getenv("SLACK_CLIENT_ID"),
		os.Getenv("SLACK_CLIENT_SECRET"),
		os.Getenv("SLACK_REDIRECT_URI"),
	)
	if err != nil {
		http.Error(w, "token exchange failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	conn := &storage.WorkspaceConnection{
		UserID:        userID,
		Provider:      "slack",
		AccessToken:   tok.userToken,
		BotToken:      tok.botToken,
		WorkspaceID:   tok.teamID,
		WorkspaceName: tok.teamName,
		Scopes:        strings.Split(tok.scope, ","),
		ConnectedAt:   time.Now().UTC(),
	}
	if err := storage.UpsertWorkspaceConnection(h.db, conn); err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/app/settings?tab=integrations&slack=connected", http.StatusFound)
}

func (h *integrationsHandlers) slackDisconnect(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	_ = storage.DeleteWorkspaceConnection(h.db, userID, "slack")
	w.WriteHeader(http.StatusNoContent)
}

func (h *integrationsHandlers) slackStatus(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	w.Header().Set("Content-Type", "application/json")
	conn, err := storage.GetWorkspaceConnection(h.db, userID, "slack")
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"connected": false})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"connected":      true,
		"workspace_name": conn.WorkspaceName,
	})
}

type slackTokenResponse struct {
	userToken string
	botToken  string
	teamID    string
	teamName  string
	scope     string
}

func slackExchangeCode(code, clientID, clientSecret, redirectURI string) (*slackTokenResponse, error) {
	form := url.Values{}
	form.Set("code", code)
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)
	form.Set("redirect_uri", redirectURI)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		"https://slack.com/api/oauth.v2.access", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var result struct {
		OK          bool   `json:"ok"`
		Error       string `json:"error"`
		AccessToken string `json:"access_token"`
		Scope       string `json:"scope"`
		Team        struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"team"`
		AuthedUser struct {
			AccessToken string `json:"access_token"`
		} `json:"authed_user"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	if !result.OK {
		return nil, &httpError{Code: http.StatusBadGateway, Msg: result.Error}
	}
	return &slackTokenResponse{
		userToken: result.AuthedUser.AccessToken,
		botToken:  result.AccessToken,
		teamID:    result.Team.ID,
		teamName:  result.Team.Name,
		scope:     result.Scope,
	}, nil
}

// ── Notion OAuth ──────────────────────────────────────────────────────────────

func (h *integrationsHandlers) notionConnect(w http.ResponseWriter, r *http.Request) {
	clientID := os.Getenv("NOTION_CLIENT_ID")
	redirectURI := os.Getenv("NOTION_REDIRECT_URI")
	if clientID == "" {
		http.Error(w, "Notion not configured", http.StatusServiceUnavailable)
		return
	}
	state := randomState()
	http.SetCookie(w, &http.Cookie{Name: "notion_oauth_state", Value: state, Path: "/", MaxAge: 300, HttpOnly: true})

	q := url.Values{}
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("owner", "user")
	q.Set("state", state)
	http.Redirect(w, r, "https://api.notion.com/v1/oauth/authorize?"+q.Encode(), http.StatusFound)
}

func (h *integrationsHandlers) notionCallback(w http.ResponseWriter, r *http.Request) {
	stateCookie, err := r.Cookie("notion_oauth_state")
	if err != nil || stateCookie.Value != r.URL.Query().Get("state") {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}
	userID := userIDFromCtx(r.Context())

	code := r.URL.Query().Get("code")
	tok, err := notionExchangeCode(code,
		os.Getenv("NOTION_CLIENT_ID"),
		os.Getenv("NOTION_CLIENT_SECRET"),
		os.Getenv("NOTION_REDIRECT_URI"),
	)
	if err != nil {
		http.Error(w, "token exchange failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	conn := &storage.WorkspaceConnection{
		UserID:        userID,
		Provider:      "notion",
		AccessToken:   tok.accessToken,
		WorkspaceID:   tok.workspaceID,
		WorkspaceName: tok.workspaceName,
		ConnectedAt:   time.Now().UTC(),
	}
	if err := storage.UpsertWorkspaceConnection(h.db, conn); err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/app/settings?tab=integrations&notion=connected", http.StatusFound)
}

func (h *integrationsHandlers) notionDisconnect(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	_ = storage.DeleteWorkspaceConnection(h.db, userID, "notion")
	w.WriteHeader(http.StatusNoContent)
}

func (h *integrationsHandlers) notionStatus(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	w.Header().Set("Content-Type", "application/json")
	conn, err := storage.GetWorkspaceConnection(h.db, userID, "notion")
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"connected": false})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"connected":      true,
		"workspace_name": conn.WorkspaceName,
	})
}

type notionTokenResponse struct {
	accessToken   string
	workspaceID   string
	workspaceName string
}

func notionExchangeCode(code, clientID, clientSecret, redirectURI string) (*notionTokenResponse, error) {
	payload := map[string]string{
		"grant_type":   "authorization_code",
		"code":         code,
		"redirect_uri": redirectURI,
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://api.notion.com/v1/oauth/token", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(clientID, clientSecret)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	var result struct {
		AccessToken   string `json:"access_token"`
		WorkspaceID   string `json:"workspace_id"`
		WorkspaceName string `json:"workspace_name"`
		Error         string `json:"error"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, err
	}
	if result.Error != "" {
		return nil, &httpError{Code: http.StatusBadGateway, Msg: result.Error}
	}
	return &notionTokenResponse{
		accessToken:   result.AccessToken,
		workspaceID:   result.WorkspaceID,
		workspaceName: result.WorkspaceName,
	}, nil
}

// httpError is a simple error type carrying an HTTP status.
type httpError struct {
	Code int
	Msg  string
}

func (e *httpError) Error() string { return e.Msg }
