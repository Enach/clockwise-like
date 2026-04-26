package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

type backendClient struct {
	baseURL string
	http    *http.Client
}

func newClient(baseURL string) *backendClient {
	return &backendClient{baseURL: baseURL, http: &http.Client{}}
}

func (c *backendClient) get(ctx context.Context, path string, query url.Values) (json.RawMessage, error) {
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("backend %d: %s", resp.StatusCode, body)
	}
	return json.RawMessage(body), nil
}

func (c *backendClient) post(ctx context.Context, path string, payload any) (json.RawMessage, error) {
	return c.doJSON(ctx, http.MethodPost, path, payload)
}

func (c *backendClient) patch(ctx context.Context, path string, payload any) (json.RawMessage, error) {
	return c.doJSON(ctx, http.MethodPatch, path, payload)
}

func (c *backendClient) delete(ctx context.Context, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("backend %d: %s", resp.StatusCode, body)
	}
	return nil
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
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("backend %d: %s", resp.StatusCode, body)
	}
	return json.RawMessage(body), nil
}
