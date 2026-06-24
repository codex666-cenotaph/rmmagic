package rmm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDoSendsBearerAndDecodesJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer rmm_secret" {
			t.Errorf("Authorization = %q", got)
		}
		if r.URL.Path != "/api/v1/devices" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"devices":[]}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "rmm_secret")
	raw, err := c.Do(context.Background(), "GET", "/api/v1/devices", nil, nil)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	var out struct {
		Devices []any `json:"devices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

func TestDoSendsJSONBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("server got non-JSON body: %s", body)
		}
		if req["tags"] == nil {
			t.Errorf("body missing tags: %v", req)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q", ct)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"tags":["prod"]}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "rmm_secret")
	_, err := c.Do(context.Background(), "PUT", "/api/v1/devices/x/tags", nil,
		map[string]any{"tags": []string{"prod"}})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
}

func TestDoSurfacesAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":"forbidden"}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "rmm_secret")
	_, err := c.Do(context.Background(), "GET", "/api/v1/audit", nil, nil)
	if err == nil {
		t.Fatal("want error for 403 response")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("want *APIError, got %T: %v", err, err)
	}
	if apiErr.Status != http.StatusForbidden || apiErr.Message != "forbidden" {
		t.Errorf("apiErr = %+v", apiErr)
	}
}

func TestDoEmptyBodyBecomesOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := New(srv.URL, "rmm_secret")
	raw, err := c.Do(context.Background(), "POST", "/api/v1/alerts/x/ack", nil, nil)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if string(raw) != `{"ok":true}` {
		t.Errorf("empty 204 body = %s, want ok object", raw)
	}
}
