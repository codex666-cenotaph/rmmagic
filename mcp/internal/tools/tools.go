// Package tools registers the rmmagic MCP tool set onto an mcp.Server.
// Each tool maps to one control-plane REST endpoint; the tool's JSON
// schema mirrors the endpoint's parameters so an AI agent can discover
// and call it without out-of-band documentation. Authorization is
// enforced server-side by the API token's permissions and scope — the
// MCP layer adds no privileges of its own.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/codex666-cenotaph/rmmagic/mcp/internal/mcp"
	"github.com/codex666-cenotaph/rmmagic/mcp/internal/rmm"
)

// Register adds every rmmagic tool to s, backed by client.
func Register(s *mcp.Server, client *rmm.Client) {
	r := registrar{s: s, c: client}

	// --- Organization ---
	r.read("rmm_list_customers", "List all customers (organizations) in the tenant.",
		nil, func(ctx context.Context, a args) (json.RawMessage, error) {
			return r.c.Do(ctx, "GET", "/api/v1/customers", nil, nil)
		})
	r.read("rmm_list_sites", "List the sites belonging to a customer.",
		props{"customer_id": strProp("Customer UUID.")}, []string{"customer_id"},
		func(ctx context.Context, a args) (json.RawMessage, error) {
			id, err := a.uuid("customer_id")
			if err != nil {
				return nil, err
			}
			return r.c.Do(ctx, "GET", "/api/v1/customers/"+id+"/sites", nil, nil)
		})

	// --- Devices ---
	r.read("rmm_list_devices", "List all devices (endpoints) the token can see, with status, OS, tags and online state.",
		nil, func(ctx context.Context, a args) (json.RawMessage, error) {
			return r.c.Do(ctx, "GET", "/api/v1/devices", nil, nil)
		})
	r.read("rmm_get_device", "Get a single device by ID.",
		props{"device_id": strProp("Device UUID.")}, []string{"device_id"},
		func(ctx context.Context, a args) (json.RawMessage, error) {
			id, err := a.uuid("device_id")
			if err != nil {
				return nil, err
			}
			return r.c.Do(ctx, "GET", "/api/v1/devices/"+id, nil, nil)
		})
	r.read("rmm_get_device_stats", "Get time-series resource stats (CPU, memory, disk) for a device. Defaults to the last hour.",
		props{
			"device_id": strProp("Device UUID."),
			"since":     strProp("Start time, RFC3339 (optional)."),
			"until":     strProp("End time, RFC3339 (optional)."),
		}, []string{"device_id"},
		func(ctx context.Context, a args) (json.RawMessage, error) {
			id, err := a.uuid("device_id")
			if err != nil {
				return nil, err
			}
			q := url.Values{}
			a.setQuery(q, "since", "until")
			return r.c.Do(ctx, "GET", "/api/v1/devices/"+id+"/stats", q, nil)
		})
	r.read("rmm_get_device_inventory", "Get hardware, installed-package and service inventory for a device.",
		props{"device_id": strProp("Device UUID.")}, []string{"device_id"},
		func(ctx context.Context, a args) (json.RawMessage, error) {
			id, err := a.uuid("device_id")
			if err != nil {
				return nil, err
			}
			return r.c.Do(ctx, "GET", "/api/v1/devices/"+id+"/inventory", nil, nil)
		})
	r.read("rmm_get_effective_policy", "Get the effective (merged) policy applied to a device.",
		props{"device_id": strProp("Device UUID.")}, []string{"device_id"},
		func(ctx context.Context, a args) (json.RawMessage, error) {
			id, err := a.uuid("device_id")
			if err != nil {
				return nil, err
			}
			return r.c.Do(ctx, "GET", "/api/v1/devices/"+id+"/effective-policy", nil, nil)
		})
	r.write("rmm_set_device_tags", "Replace the tag set on a device (lower-case, 1-32 chars of a-z/0-9/-/_, max 20). Requires devices.manage.",
		props{
			"device_id": strProp("Device UUID."),
			"tags": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Full replacement tag list.",
			},
		}, []string{"device_id", "tags"},
		func(ctx context.Context, a args) (json.RawMessage, error) {
			id, err := a.uuid("device_id")
			if err != nil {
				return nil, err
			}
			tags, err := a.stringSlice("tags")
			if err != nil {
				return nil, err
			}
			return r.c.Do(ctx, "PUT", "/api/v1/devices/"+id+"/tags", nil, map[string]any{"tags": tags})
		})

	// --- Scripts & jobs ---
	r.read("rmm_list_scripts", "List scripts in the library.",
		props{"archived": boolProp("Include archived scripts (default false).")}, nil,
		func(ctx context.Context, a args) (json.RawMessage, error) {
			q := url.Values{}
			if a.boolVal("archived") {
				q.Set("archived", "true")
			}
			return r.c.Do(ctx, "GET", "/api/v1/scripts", q, nil)
		})
	r.read("rmm_get_script", "Get a script's details including its body.",
		props{"script_id": strProp("Script UUID.")}, []string{"script_id"},
		func(ctx context.Context, a args) (json.RawMessage, error) {
			id, err := a.uuid("script_id")
			if err != nil {
				return nil, err
			}
			return r.c.Do(ctx, "GET", "/api/v1/scripts/"+id, nil, nil)
		})
	r.write("rmm_dispatch_script",
		"Run a script on one device. Returns the created job ID(s). Requires scripts.execute. "+
			"If the resolved target exceeds the mass-action blast radius the API returns "+
			"confirmation_required with a confirm_token; call again passing that token to proceed.",
		props{
			"script_id":     strProp("Script UUID to run."),
			"device_id":     strProp("Target device UUID (single-device shorthand)."),
			"parameters":    objProp("Script parameters as a JSON object (optional)."),
			"timeout_s":     intProp("Per-device execution timeout in seconds (default 300, max 86400)."),
			"confirm_token": strProp("Confirmation token echoed from a prior confirmation_required response (optional)."),
		}, []string{"script_id", "device_id"},
		func(ctx context.Context, a args) (json.RawMessage, error) {
			id, err := a.uuid("script_id")
			if err != nil {
				return nil, err
			}
			devID, err := a.uuid("device_id")
			if err != nil {
				return nil, err
			}
			body := map[string]any{"device_id": devID}
			if v, ok := a["parameters"]; ok {
				body["parameters"] = v
			}
			if n := a.intVal("timeout_s"); n > 0 {
				body["timeout_s"] = n
			}
			if t := a.strVal("confirm_token"); t != "" {
				body["confirm_token"] = t
			}
			return r.c.Do(ctx, "POST", "/api/v1/scripts/"+id+"/dispatch", nil, body)
		})
	r.read("rmm_list_jobs", "List recent script jobs, optionally filtered by device.",
		props{"device_id": strProp("Filter to a single device UUID (optional).")}, nil,
		func(ctx context.Context, a args) (json.RawMessage, error) {
			q := url.Values{}
			a.setQuery(q, "device_id")
			return r.c.Do(ctx, "GET", "/api/v1/jobs", q, nil)
		})
	r.read("rmm_get_job", "Get a job's status and metadata.",
		props{"job_id": strProp("Job UUID.")}, []string{"job_id"},
		func(ctx context.Context, a args) (json.RawMessage, error) {
			id, err := a.uuid("job_id")
			if err != nil {
				return nil, err
			}
			return r.c.Do(ctx, "GET", "/api/v1/jobs/"+id, nil, nil)
		})
	r.read("rmm_get_job_output", "Get a finished job's stdout/stderr output and exit code.",
		props{"job_id": strProp("Job UUID.")}, []string{"job_id"},
		func(ctx context.Context, a args) (json.RawMessage, error) {
			id, err := a.uuid("job_id")
			if err != nil {
				return nil, err
			}
			return r.c.Do(ctx, "GET", "/api/v1/jobs/"+id+"/output", nil, nil)
		})

	// --- Schedules & policies ---
	r.read("rmm_list_schedules", "List recurring script schedules.",
		nil, func(ctx context.Context, a args) (json.RawMessage, error) {
			return r.c.Do(ctx, "GET", "/api/v1/schedules", nil, nil)
		})
	r.read("rmm_list_policies", "List monitoring/configuration policies.",
		nil, func(ctx context.Context, a args) (json.RawMessage, error) {
			return r.c.Do(ctx, "GET", "/api/v1/policies", nil, nil)
		})

	// --- Alerts ---
	r.read("rmm_list_alerts", "List alerts, optionally filtered by status (e.g. open) and/or device.",
		props{
			"status":    strProp("Filter by status, e.g. \"open\" or \"acknowledged\" (optional)."),
			"device_id": strProp("Filter to a single device UUID (optional)."),
			"limit":     intProp("Max results, 1-1000 (default 200)."),
		}, nil,
		func(ctx context.Context, a args) (json.RawMessage, error) {
			q := url.Values{}
			a.setQuery(q, "status", "device_id")
			if n := a.intVal("limit"); n > 0 {
				q.Set("limit", fmt.Sprintf("%d", n))
			}
			return r.c.Do(ctx, "GET", "/api/v1/alerts", q, nil)
		})
	r.read("rmm_get_alert", "Get a single alert by ID.",
		props{"alert_id": strProp("Alert UUID.")}, []string{"alert_id"},
		func(ctx context.Context, a args) (json.RawMessage, error) {
			id, err := a.uuid("alert_id")
			if err != nil {
				return nil, err
			}
			return r.c.Do(ctx, "GET", "/api/v1/alerts/"+id, nil, nil)
		})
	r.write("rmm_ack_alert", "Acknowledge an alert. Requires alerts.manage.",
		props{"alert_id": strProp("Alert UUID.")}, []string{"alert_id"},
		func(ctx context.Context, a args) (json.RawMessage, error) {
			id, err := a.uuid("alert_id")
			if err != nil {
				return nil, err
			}
			return r.c.Do(ctx, "POST", "/api/v1/alerts/"+id+"/ack", nil, nil)
		})

	// --- Audit ---
	r.read("rmm_list_audit", "List recent audit-log entries. Requires audit.read.",
		nil, func(ctx context.Context, a args) (json.RawMessage, error) {
			return r.c.Do(ctx, "GET", "/api/v1/audit", nil, nil)
		})
}

// registrar wires tool handlers to an mcp.Server, rendering each handler's
// JSON result as pretty-printed text content.
type registrar struct {
	s *mcp.Server
	c *rmm.Client
}

type handlerFunc func(ctx context.Context, a args) (json.RawMessage, error)

// read registers a tool. The variadic tail optionally carries the
// required-property list ([]string); omit it for tools with no required
// args. props may be nil for tools that take no arguments.
func (r registrar) read(name, desc string, p props, rest ...any) {
	required, h := splitRest(rest)
	r.s.RegisterTool(name, desc, schema(p, required), r.wrap(h))
}

// write is identical to read but exists to document, at the call site,
// that the tool mutates state. Both go through the same path; the API
// token's permissions decide what is actually allowed.
func (r registrar) write(name, desc string, p props, rest ...any) {
	required, h := splitRest(rest)
	r.s.RegisterTool(name, desc, schema(p, required), r.wrap(h))
}

func splitRest(rest []any) ([]string, handlerFunc) {
	var required []string
	var h handlerFunc
	for _, v := range rest {
		switch t := v.(type) {
		case []string:
			required = t
		case func(context.Context, args) (json.RawMessage, error):
			h = t
		}
	}
	return required, h
}

func (r registrar) wrap(h handlerFunc) mcp.ToolHandler {
	return func(ctx context.Context, raw map[string]any) (string, error) {
		out, err := h(ctx, args(raw))
		if err != nil {
			return "", err
		}
		var pretty json.RawMessage
		var buf strings.Builder
		enc := json.NewEncoder(&buf)
		enc.SetIndent("", "  ")
		if json.Unmarshal(out, &pretty) == nil && enc.Encode(pretty) == nil {
			return strings.TrimRight(buf.String(), "\n"), nil
		}
		return string(out), nil
	}
}
