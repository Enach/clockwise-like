package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type backendClient struct {
	baseURL string
	http    *http.Client
}

// backendCallTimeout bounds every call to the backend. Without it a backend
// that accepts a connection and never answers hangs the tool call for as long
// as the MCP client waits, whatever context it passed (hygiene, security
// audit of 2026-11-22; the same rule as backend/internal/outbound).
const backendCallTimeout = 30 * time.Second

func newClient(baseURL string) *backendClient {
	return &backendClient{baseURL: baseURL, http: &http.Client{Timeout: backendCallTimeout}}
}

// pathSegment escapes one path segment of a backend route. An id is taken
// from the tool call, so unescaped interpolation let an id like
// "../book/slug" steer the request to another backend path (hygiene,
// security audit of 2026-11-22: every tool targets a requireAuth route, so
// such a request answers 401 today).
func pathSegment(s string) string { return url.PathEscape(s) }

func (c *backendClient) get(ctx context.Context, path string, query url.Values) (json.RawMessage, error) {
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	return c.do(req)
}

func (c *backendClient) post(ctx context.Context, path string, payload any) (json.RawMessage, error) {
	return c.doJSON(ctx, http.MethodPost, path, payload)
}

func (c *backendClient) patch(ctx context.Context, path string, payload any) (json.RawMessage, error) {
	return c.doJSON(ctx, http.MethodPatch, path, payload)
}

// delete returns the backend's body too: DELETE /api/focus/blocks answers
// {deleted: n} (PAC-122 SF-064).
func (c *backendClient) delete(ctx context.Context, path string) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	return c.do(req)
}

func (c *backendClient) doJSON(ctx context.Context, method, path string, payload any) (json.RawMessage, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req)
}

// do sends req and returns the whole body. A body that cannot be read in
// full is an error, never a success carrying part of it (PAC-122 SF-095).
func (c *backendClient) do(req *http.Request) (json.RawMessage, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("backend %d: read body: %w", resp.StatusCode, err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("backend %d: %s", resp.StatusCode, body)
	}
	return json.RawMessage(body), nil
}
