package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newHTTPTestServer() http.Handler {
	s := NewServer("rmmagic", "test")
	s.RegisterTool("whoami", "returns the token from context",
		map[string]any{"type": "object"},
		func(ctx context.Context, _ map[string]any) (string, error) {
			tok, _ := ctx.Value(testTokenKey).(string)
			if tok == "" {
				return "anonymous", nil
			}
			return "token:" + tok, nil
		})
	return s.HTTPHandler(func(ctx context.Context, r *http.Request) context.Context {
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			return context.WithValue(ctx, testTokenKey, strings.TrimPrefix(h, "Bearer "))
		}
		return ctx
	})
}

type testKey int

const testTokenKey testKey = 0

func post(t *testing.T, h http.Handler, body string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHTTPInitializeReturnsSessionID(t *testing.T) {
	h := newHTTPTestServer()
	rec := post(t, h, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Header().Get("Mcp-Session-Id") == "" {
		t.Error("initialize did not return an Mcp-Session-Id")
	}
	var resp rpcResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Result.(map[string]any)["serverInfo"] == nil {
		t.Error("missing serverInfo")
	}
}

func TestHTTPNotificationReturns202(t *testing.T) {
	h := newHTTPTestServer()
	rec := post(t, h, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
	if strings.TrimSpace(rec.Body.String()) != "" {
		t.Errorf("notification produced a body: %q", rec.Body.String())
	}
}

func TestHTTPPerRequestTokenReachesTool(t *testing.T) {
	h := newHTTPTestServer()
	rec := post(t, h,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"whoami","arguments":{}}}`,
		map[string]string{"Authorization": "Bearer rmm_alice"})
	var resp rpcResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	text := resp.Result.(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if text != "token:rmm_alice" {
		t.Errorf("tool did not see per-request token; got %q", text)
	}
}

func TestHTTPBatch(t *testing.T) {
	h := newHTTPTestServer()
	rec := post(t, h,
		`[{"jsonrpc":"2.0","id":1,"method":"ping"},{"jsonrpc":"2.0","id":2,"method":"tools/list"}]`, nil)
	var resps []rpcResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resps); err != nil {
		t.Fatalf("decode batch: %v", err)
	}
	if len(resps) != 2 {
		t.Fatalf("want 2 responses, got %d", len(resps))
	}
}

func TestHTTPGetIs405(t *testing.T) {
	h := newHTTPTestServer()
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want 405", rec.Code)
	}
}

func TestHTTPParseError(t *testing.T) {
	h := newHTTPTestServer()
	rec := post(t, h, `{not json`, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
