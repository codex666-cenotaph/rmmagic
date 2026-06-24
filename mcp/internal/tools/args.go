package tools

import (
	"fmt"
	"net/url"
	"regexp"
)

// uuidPattern matches the canonical 8-4-4-4-12 hex form. Validation is
// case-insensitive; we only guard against obviously malformed IDs so the
// agent gets a clear message instead of a server-side 404.
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// args is the decoded "arguments" object of a tool call with typed,
// validating accessors. JSON numbers decode to float64, which the
// integer helpers account for.
type args map[string]any

func (a args) strVal(key string) string {
	if v, ok := a[key].(string); ok {
		return v
	}
	return ""
}

func (a args) boolVal(key string) bool {
	b, _ := a[key].(bool)
	return b
}

func (a args) intVal(key string) int {
	switch n := a[key].(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}

// uuid returns the value at key validated as a UUID, erroring if it is
// missing, the wrong type, or malformed. UUIDs are validated client-side
// so a bad ID yields a clear message instead of a server-side 400/404.
func (a args) uuid(key string) (string, error) {
	s, ok := a[key].(string)
	if !ok || s == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	if !uuidPattern.MatchString(s) {
		return "", fmt.Errorf("%s is not a valid UUID: %q", key, s)
	}
	return s, nil
}

// stringSlice returns the value at key as a []string, accepting a JSON
// array of strings.
func (a args) stringSlice(key string) ([]string, error) {
	raw, ok := a[key].([]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an array of strings", key)
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("%s must contain only strings", key)
		}
		out = append(out, s)
	}
	return out, nil
}

// setQuery copies the given string-valued args into q, skipping any that
// are absent or empty.
func (a args) setQuery(q url.Values, keys ...string) {
	for _, k := range keys {
		if v := a.strVal(k); v != "" {
			q.Set(k, v)
		}
	}
}

// --- JSON Schema helpers for tool input definitions ---

type props map[string]any

func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}
func boolProp(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}
func intProp(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}
func objProp(desc string) map[string]any {
	return map[string]any{"type": "object", "description": desc}
}

// schema builds a JSON Schema object for a tool's inputs. Properties may
// be nil (no arguments); required may be nil (none required).
func schema(p props, required []string) map[string]any {
	properties := map[string]any{}
	for k, v := range p {
		properties[k] = v
	}
	s := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}
