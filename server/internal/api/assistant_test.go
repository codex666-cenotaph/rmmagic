package api

import (
	"encoding/json"
	"testing"
)

// findTool returns the assistant tool with the given model-facing name.
func findTool(t *testing.T, name string) assistantTool {
	t.Helper()
	for _, tl := range assistantTools() {
		if tl.def.OfTool.Name == name {
			return tl
		}
	}
	t.Fatalf("tool %q not registered", name)
	return assistantTool{}
}

func TestAssistantToolBuildsPaths(t *testing.T) {
	cases := []struct {
		tool       string
		args       map[string]any
		wantMethod string
		wantPath   string
	}{
		{"list_devices", nil, "GET", "/api/v1/devices"},
		{"get_device", map[string]any{"device_id": "11111111-1111-1111-1111-111111111111"},
			"GET", "/api/v1/devices/11111111-1111-1111-1111-111111111111"},
		{"list_sites", map[string]any{"customer_id": "22222222-2222-2222-2222-222222222222"},
			"GET", "/api/v1/customers/22222222-2222-2222-2222-222222222222/sites"},
		{"ack_alert", map[string]any{"alert_id": "33333333-3333-3333-3333-333333333333"},
			"POST", "/api/v1/alerts/33333333-3333-3333-3333-333333333333/ack"},
		{"list_alerts", map[string]any{"status": "firing"}, "GET", "/api/v1/alerts?status=firing"},
	}
	for _, c := range cases {
		tl := findTool(t, c.tool)
		method, path, _, err := tl.build(c.args)
		if err != nil {
			t.Errorf("%s: unexpected error %v", c.tool, err)
			continue
		}
		if method != c.wantMethod || path != c.wantPath {
			t.Errorf("%s: got %s %s, want %s %s", c.tool, method, path, c.wantMethod, c.wantPath)
		}
	}
}

func TestAssistantToolRejectsBadUUID(t *testing.T) {
	tl := findTool(t, "get_device")
	if _, _, _, err := tl.build(map[string]any{"device_id": "not-a-uuid"}); err == nil {
		t.Error("expected error for non-UUID device_id")
	}
	if _, _, _, err := tl.build(map[string]any{}); err == nil {
		t.Error("expected error for missing device_id")
	}
}

func TestAssistantDispatchBuildsBody(t *testing.T) {
	tl := findTool(t, "dispatch_script")
	method, path, body, err := tl.build(map[string]any{
		"script_id":  "44444444-4444-4444-4444-444444444444",
		"device_id":  "55555555-5555-5555-5555-555555555555",
		"timeout_s":  float64(600),
		"parameters": map[string]any{"key": "val"},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if method != "POST" || path != "/api/v1/scripts/44444444-4444-4444-4444-444444444444/dispatch" {
		t.Fatalf("got %s %s", method, path)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("body not JSON: %s", body)
	}
	if got["device_id"] != "55555555-5555-5555-5555-555555555555" {
		t.Errorf("device_id missing: %v", got)
	}
	if got["timeout_s"] != float64(600) {
		t.Errorf("timeout_s missing: %v", got)
	}
}

func TestAssistantToolsAreUniqueAndValid(t *testing.T) {
	seen := map[string]bool{}
	for _, tl := range assistantTools() {
		name := tl.def.OfTool.Name
		if name == "" {
			t.Error("tool with empty name")
		}
		if seen[name] {
			t.Errorf("duplicate tool name %q", name)
		}
		seen[name] = true
		if tl.build == nil {
			t.Errorf("tool %q has no build function", name)
		}
	}
}
