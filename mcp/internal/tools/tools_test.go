package tools

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/codex666-cenotaph/rmmagic/mcp/internal/mcp"
	"github.com/codex666-cenotaph/rmmagic/mcp/internal/rmm"
)

// fixedClient returns a ClientFor that always uses the given base URL and
// a test token.
func fixedClient(baseURL string) ClientFor {
	return func(context.Context) (*rmm.Client, error) {
		return rmm.New(baseURL, "rmm_x"), nil
	}
}

func TestRegisterAdvertisesToolsOverProtocol(t *testing.T) {
	s := mcp.NewServer("rmmagic", "test")
	Register(s, fixedClient("http://example.invalid"))

	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}` + "\n")
	var out strings.Builder
	if err := s.Serve(context.Background(), in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	for _, want := range []string{
		"rmm_list_devices", "rmm_dispatch_script", "rmm_get_job_output",
		"rmm_list_alerts", "rmm_ack_alert", "rmm_set_device_tags",
	} {
		if !strings.Contains(out.String(), `"`+want+`"`) {
			t.Errorf("tools/list missing %s", want)
		}
	}
}

func TestDispatchScriptValidatesUUIDClientSide(t *testing.T) {
	var hit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	s := mcp.NewServer("rmmagic", "test")
	Register(s, fixedClient(srv.URL))

	// device_id is not a UUID: should fail before any HTTP call.
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":` +
		`{"name":"rmm_dispatch_script","arguments":{"script_id":"11111111-1111-1111-1111-111111111111","device_id":"not-a-uuid"}}}`
	var out strings.Builder
	if err := s.Serve(context.Background(), strings.NewReader(req+"\n"), &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if hit {
		t.Error("client called the API despite an invalid UUID argument")
	}
	if !strings.Contains(out.String(), "not a valid UUID") {
		t.Errorf("expected UUID validation error, got %s", out.String())
	}
	if !strings.Contains(out.String(), `"isError":true`) {
		t.Errorf("expected isError true, got %s", out.String())
	}
}

func TestListDevicesCallsEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/devices" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"devices":[{"hostname":"web-1"}]}`)
	}))
	defer srv.Close()

	s := mcp.NewServer("rmmagic", "test")
	Register(s, fixedClient(srv.URL))

	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"rmm_list_devices","arguments":{}}}`
	var out strings.Builder
	if err := s.Serve(context.Background(), strings.NewReader(req+"\n"), &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if !strings.Contains(out.String(), "web-1") {
		t.Errorf("response missing device data: %s", out.String())
	}
}
