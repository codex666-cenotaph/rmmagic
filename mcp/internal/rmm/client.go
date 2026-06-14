// Package rmm is a thin client for the rmmagic control-plane REST API.
// It authenticates with a tenant API token ("rmm_...") via the standard
// Authorization: Bearer header and returns decoded JSON, surfacing the
// API's own error messages so they can be relayed to an AI agent.
package rmm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client talks to a single rmmagic instance with a single API token.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// New returns a client for baseURL (e.g. "https://rmm.example.com")
// authenticating with the given "rmm_..." API token.
func New(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// APIError is returned when the API responds with a non-2xx status. It
// carries the HTTP status and the server's error message verbatim.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("API returned HTTP %d", e.Status)
	}
	return fmt.Sprintf("HTTP %d: %s", e.Status, e.Message)
}

// Do performs a request against path (e.g. "/api/v1/devices"), attaching
// query and an optional JSON body. It returns the raw response body for
// 2xx responses and an *APIError otherwise.
func (c *Client) Do(ctx context.Context, method, path string, query url.Values, body any) (json.RawMessage, error) {
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode request body: %w", err)
		}
		reader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &APIError{Status: resp.StatusCode, Message: extractError(raw)}
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		// 204 No Content and similar: present an explicit success object.
		return json.RawMessage(`{"ok":true}`), nil
	}
	return json.RawMessage(raw), nil
}

// extractError pulls the {"error": "..."} message the API returns, or
// falls back to the raw body trimmed to a sane length.
func extractError(raw []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Error != "" {
		return e.Error
	}
	s := strings.TrimSpace(string(raw))
	if len(s) > 500 {
		s = s[:500] + "…"
	}
	return s
}
