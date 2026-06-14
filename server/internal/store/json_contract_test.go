package store

import (
	"encoding/json"
	"testing"
)

// These structs are serialized directly to API clients (no DTO layer), so a
// missing json tag silently ships PascalCase field names and crashes the
// dashboard pages that read snake_case. Lock the wire contract here so the
// regression can't recur unnoticed.

func assertJSONKeys(t *testing.T, v any, keys ...string) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			t.Errorf("missing JSON key %q in %T (got %v)", k, v, m)
		}
	}
}

func TestPolicyJSONContract(t *testing.T) {
	assertJSONKeys(t, Policy{}, "id", "name", "scope_type", "scope_id",
		"enabled", "rules", "channel_ids", "created_at", "updated_at")
}

func TestAlertJSONContract(t *testing.T) {
	assertJSONKeys(t, Alert{}, "id", "device_id", "hostname", "site_id",
		"customer_id", "policy_id", "rule_type", "dedup_key", "severity",
		"message", "details", "channel_ids", "status", "fired_at",
		"resolved_at", "acked_by", "acked_at")
}

func TestAppPackageJSONContract(t *testing.T) {
	assertJSONKeys(t, AppPackage{}, "id", "name", "description", "os",
		"install", "detection", "timeout_s", "archived", "created_at", "updated_at")
}

func TestDeploymentRuleJSONContract(t *testing.T) {
	assertJSONKeys(t, DeploymentRule{}, "id", "package_id", "package_name",
		"package_os", "name", "scope_type", "scope_id", "filters", "enabled",
		"last_run_at", "created_at", "updated_at")
}
