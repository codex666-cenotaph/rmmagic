package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// roundtrip feeds the given newline-delimited requests through the server
// and returns one decoded response per non-notification line, in order.
func roundtrip(t *testing.T, s *Server, requests ...string) []rpcResponse {
	t.Helper()
	in := strings.NewReader(strings.Join(requests, "\n") + "\n")
	var out strings.Builder
	if err := s.Serve(context.Background(), in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	var resps []rpcResponse
	sc := bufio.NewScanner(strings.NewReader(out.String()))
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == "" {
			continue
		}
		var r rpcResponse
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatalf("decode response %q: %v", sc.Text(), err)
		}
		resps = append(resps, r)
	}
	return resps
}

func TestInitializeEchoesSupportedVersion(t *testing.T) {
	s := NewServer("test", "1.0.0")
	resps := roundtrip(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`)
	if len(resps) != 1 {
		t.Fatalf("want 1 response, got %d", len(resps))
	}
	result := resps[0].Result.(map[string]any)
	if got := result["protocolVersion"]; got != "2025-03-26" {
		t.Errorf("protocolVersion = %v, want echo of client version", got)
	}
	if _, ok := result["capabilities"].(map[string]any)["tools"]; !ok {
		t.Error("server did not advertise tools capability")
	}
}

func TestInitializeFallsBackOnUnknownVersion(t *testing.T) {
	s := NewServer("test", "1.0.0")
	resps := roundtrip(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`)
	result := resps[0].Result.(map[string]any)
	if got := result["protocolVersion"]; got != ProtocolVersion {
		t.Errorf("protocolVersion = %v, want default %s", got, ProtocolVersion)
	}
}

func TestNotificationProducesNoResponse(t *testing.T) {
	s := NewServer("test", "1.0.0")
	resps := roundtrip(t, s, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if len(resps) != 0 {
		t.Fatalf("notification must not produce a response, got %d", len(resps))
	}
}

func TestToolsListReturnsRegisteredTools(t *testing.T) {
	s := NewServer("test", "1.0.0")
	s.RegisterTool("alpha", "first", map[string]any{"type": "object"},
		func(context.Context, map[string]any) (string, error) { return "", nil })
	s.RegisterTool("beta", "second", map[string]any{"type": "object"},
		func(context.Context, map[string]any) (string, error) { return "", nil })

	resps := roundtrip(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	tools := resps[0].Result.(map[string]any)["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("want 2 tools, got %d", len(tools))
	}
	// Registration order is preserved.
	if tools[0].(map[string]any)["name"] != "alpha" {
		t.Errorf("tools out of order: %v", tools)
	}
}

func TestToolsCallSuccess(t *testing.T) {
	s := NewServer("test", "1.0.0")
	s.RegisterTool("echo", "echoes name", map[string]any{"type": "object"},
		func(_ context.Context, a map[string]any) (string, error) {
			return "hello " + a["name"].(string), nil
		})
	resps := roundtrip(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"name":"world"}}}`)
	result := resps[0].Result.(map[string]any)
	if result["isError"] != false {
		t.Errorf("isError = %v, want false", result["isError"])
	}
	content := result["content"].([]any)[0].(map[string]any)
	if content["text"] != "hello world" {
		t.Errorf("text = %v", content["text"])
	}
}

func TestToolsCallErrorIsInBand(t *testing.T) {
	s := NewServer("test", "1.0.0")
	s.RegisterTool("boom", "fails", map[string]any{"type": "object"},
		func(context.Context, map[string]any) (string, error) {
			return "", errors.New("kaboom")
		})
	resps := roundtrip(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"boom","arguments":{}}}`)
	if resps[0].Error != nil {
		t.Fatalf("execution error must be in-band, got protocol error %+v", resps[0].Error)
	}
	result := resps[0].Result.(map[string]any)
	if result["isError"] != true {
		t.Errorf("isError = %v, want true", result["isError"])
	}
	text := result["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "kaboom") {
		t.Errorf("error text = %q, want it to contain the cause", text)
	}
}

func TestUnknownMethodReturnsProtocolError(t *testing.T) {
	s := NewServer("test", "1.0.0")
	resps := roundtrip(t, s, `{"jsonrpc":"2.0","id":1,"method":"does/not/exist"}`)
	if resps[0].Error == nil || resps[0].Error.Code != codeMethodNotFound {
		t.Fatalf("want method-not-found error, got %+v", resps[0])
	}
}

func TestUnknownToolReturnsProtocolError(t *testing.T) {
	s := NewServer("test", "1.0.0")
	resps := roundtrip(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"nope","arguments":{}}}`)
	if resps[0].Error == nil || resps[0].Error.Code != codeMethodNotFound {
		t.Fatalf("want method-not-found for unknown tool, got %+v", resps[0])
	}
}

func TestParseErrorOnGarbage(t *testing.T) {
	s := NewServer("test", "1.0.0")
	resps := roundtrip(t, s, `{not json`)
	if resps[0].Error == nil || resps[0].Error.Code != codeParseError {
		t.Fatalf("want parse error, got %+v", resps[0])
	}
}
