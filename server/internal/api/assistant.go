package api

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// Assistant holds the configuration for the in-dashboard AI assistant.
// It is created from RMM_ANTHROPIC_API_KEY in main; when nil the chat
// endpoint reports that the feature is not configured.
type Assistant struct {
	Client anthropic.Client
	Model  anthropic.Model
}

// NewAssistant builds an Assistant from an Anthropic API key. model may
// be empty to use the default.
func NewAssistant(apiKey, model string) *Assistant {
	m := anthropic.Model(model)
	if m == "" {
		m = anthropic.ModelClaudeOpus4_8
	}
	return &Assistant{
		Client: anthropic.NewClient(option.WithAPIKey(apiKey)),
		Model:  m,
	}
}

// assistantSystemPrompt frames the model as an operations assistant and,
// per Opus 4.8 guidance, grants autonomy on reads while keeping it
// cautious about state-changing actions.
const assistantSystemPrompt = `You are the rmmagic assistant, embedded in an RMM (Remote Monitoring & Management) dashboard for an MSP. You help technicians inspect and manage their fleet of monitored devices by calling the provided tools.

Guidance:
- Use the read tools freely to answer questions about devices, alerts, jobs, scripts, and policies. Prefer calling a tool over guessing.
- Every tool runs with the signed-in user's own permissions; a tool that returns a "not found" or "forbidden" style error means the user lacks access — relay that rather than retrying.
- For state-changing actions (running a script, acknowledging an alert, changing tags), confirm the specific target with the user before acting unless they already asked for that exact action.
- When running a script, if the API responds that confirmation is required because the action affects many devices, surface the device count to the user and only proceed once they confirm — passing the returned confirm_token.
- Lead with the answer. Be concise; render IDs and hostnames plainly. Don't dump raw JSON at the user — summarize it.`

// assistantTool binds a model-facing tool to an internal API call.
type assistantTool struct {
	def anthropic.ToolUnionParam
	// build turns the model's arguments into an internal request.
	build func(args map[string]any) (method, path string, body []byte, err error)
}

var assistantUUIDRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func argString(a map[string]any, key string) string {
	s, _ := a[key].(string)
	return s
}

func argUUID(a map[string]any, key string) (string, error) {
	s, _ := a[key].(string)
	if s == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	if !assistantUUIDRe.MatchString(s) {
		return "", fmt.Errorf("%s is not a valid UUID: %q", key, s)
	}
	return s, nil
}

func argInt(a map[string]any, key string) int {
	switch n := a[key].(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}

func tool(name, desc string, props map[string]any, required []string) anthropic.ToolUnionParam {
	return anthropic.ToolUnionParam{OfTool: &anthropic.ToolParam{
		Name:        name,
		Description: anthropic.String(desc),
		InputSchema: anthropic.ToolInputSchemaParam{Properties: props, Required: required},
	}}
}

func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}
func intProp(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}
func objProp(desc string) map[string]any {
	return map[string]any{"type": "object", "description": desc}
}

// assistantTools is the tool set the assistant exposes. Each maps to one
// dashboard API endpoint; authorization is enforced by that endpoint
// against the chatting user's principal.
func assistantTools() []assistantTool {
	return []assistantTool{
		{
			def: tool("list_customers", "List all customers (organizations) in the tenant.", map[string]any{}, nil),
			build: func(map[string]any) (string, string, []byte, error) {
				return "GET", "/api/v1/customers", nil, nil
			},
		},
		{
			def: tool("list_sites", "List the sites belonging to a customer.",
				map[string]any{"customer_id": strProp("Customer UUID.")}, []string{"customer_id"}),
			build: func(a map[string]any) (string, string, []byte, error) {
				id, err := argUUID(a, "customer_id")
				return "GET", "/api/v1/customers/" + id + "/sites", nil, err
			},
		},
		{
			def: tool("list_devices", "List all devices the user can see, with status, OS, tags and online state.", map[string]any{}, nil),
			build: func(map[string]any) (string, string, []byte, error) {
				return "GET", "/api/v1/devices", nil, nil
			},
		},
		{
			def: tool("get_device", "Get a single device by ID.",
				map[string]any{"device_id": strProp("Device UUID.")}, []string{"device_id"}),
			build: func(a map[string]any) (string, string, []byte, error) {
				id, err := argUUID(a, "device_id")
				return "GET", "/api/v1/devices/" + id, nil, err
			},
		},
		{
			def: tool("get_device_stats", "Get time-series CPU/memory/disk stats for a device (defaults to the last hour).",
				map[string]any{
					"device_id": strProp("Device UUID."),
					"since":     strProp("Start time, RFC3339 (optional)."),
					"until":     strProp("End time, RFC3339 (optional)."),
				}, []string{"device_id"}),
			build: func(a map[string]any) (string, string, []byte, error) {
				id, err := argUUID(a, "device_id")
				if err != nil {
					return "", "", nil, err
				}
				q := url.Values{}
				if v := argString(a, "since"); v != "" {
					q.Set("since", v)
				}
				if v := argString(a, "until"); v != "" {
					q.Set("until", v)
				}
				return "GET", "/api/v1/devices/" + id + "/stats" + withQuery(q), nil, nil
			},
		},
		{
			def: tool("get_device_inventory", "Get hardware, package, and service inventory for a device.",
				map[string]any{"device_id": strProp("Device UUID.")}, []string{"device_id"}),
			build: func(a map[string]any) (string, string, []byte, error) {
				id, err := argUUID(a, "device_id")
				return "GET", "/api/v1/devices/" + id + "/inventory", nil, err
			},
		},
		{
			def: tool("set_device_tags", "Replace the full tag set on a device (requires devices.manage).",
				map[string]any{
					"device_id": strProp("Device UUID."),
					"tags": map[string]any{"type": "array", "items": map[string]any{"type": "string"},
						"description": "Full replacement tag list (lower-case, 1-32 chars of a-z/0-9/-/_)."},
				}, []string{"device_id", "tags"}),
			build: func(a map[string]any) (string, string, []byte, error) {
				id, err := argUUID(a, "device_id")
				if err != nil {
					return "", "", nil, err
				}
				b, _ := json.Marshal(map[string]any{"tags": a["tags"]})
				return "PUT", "/api/v1/devices/" + id + "/tags", b, nil
			},
		},
		{
			def: tool("list_scripts", "List scripts in the library.", map[string]any{}, nil),
			build: func(map[string]any) (string, string, []byte, error) {
				return "GET", "/api/v1/scripts", nil, nil
			},
		},
		{
			def: tool("dispatch_script",
				"Run a script on one device. Returns the created job ID(s). Requires scripts.execute. "+
					"If the API responds with confirmation_required and a confirm_token, call again with that token to proceed.",
				map[string]any{
					"script_id":     strProp("Script UUID to run."),
					"device_id":     strProp("Target device UUID."),
					"parameters":    objProp("Script parameters as a JSON object (optional)."),
					"timeout_s":     intProp("Per-device timeout in seconds (default 300)."),
					"confirm_token": strProp("Confirmation token from a prior confirmation_required response (optional)."),
				}, []string{"script_id", "device_id"}),
			build: func(a map[string]any) (string, string, []byte, error) {
				sid, err := argUUID(a, "script_id")
				if err != nil {
					return "", "", nil, err
				}
				did, err := argUUID(a, "device_id")
				if err != nil {
					return "", "", nil, err
				}
				body := map[string]any{"device_id": did}
				if v, ok := a["parameters"]; ok {
					body["parameters"] = v
				}
				if n := argInt(a, "timeout_s"); n > 0 {
					body["timeout_s"] = n
				}
				if t := argString(a, "confirm_token"); t != "" {
					body["confirm_token"] = t
				}
				b, _ := json.Marshal(body)
				return "POST", "/api/v1/scripts/" + sid + "/dispatch", b, nil
			},
		},
		{
			def: tool("list_jobs", "List recent script jobs, optionally filtered by device.",
				map[string]any{"device_id": strProp("Filter to a single device UUID (optional).")}, nil),
			build: func(a map[string]any) (string, string, []byte, error) {
				q := url.Values{}
				if v := argString(a, "device_id"); v != "" {
					q.Set("device_id", v)
				}
				return "GET", "/api/v1/jobs" + withQuery(q), nil, nil
			},
		},
		{
			def: tool("get_job", "Get a job's status and metadata.",
				map[string]any{"job_id": strProp("Job UUID.")}, []string{"job_id"}),
			build: func(a map[string]any) (string, string, []byte, error) {
				id, err := argUUID(a, "job_id")
				return "GET", "/api/v1/jobs/" + id, nil, err
			},
		},
		{
			def: tool("get_job_output", "Get a finished job's stdout/stderr and exit code.",
				map[string]any{"job_id": strProp("Job UUID.")}, []string{"job_id"}),
			build: func(a map[string]any) (string, string, []byte, error) {
				id, err := argUUID(a, "job_id")
				return "GET", "/api/v1/jobs/" + id + "/output", nil, err
			},
		},
		{
			def: tool("list_policies", "List monitoring/configuration policies.", map[string]any{}, nil),
			build: func(map[string]any) (string, string, []byte, error) {
				return "GET", "/api/v1/policies", nil, nil
			},
		},
		{
			def: tool("list_alerts", "List alerts, optionally filtered by status (e.g. firing) and/or device.",
				map[string]any{
					"status":    strProp("Filter by status, e.g. \"firing\" or \"resolved\" (optional)."),
					"device_id": strProp("Filter to a single device UUID (optional)."),
					"limit":     intProp("Max results, 1-1000 (default 200)."),
				}, nil),
			build: func(a map[string]any) (string, string, []byte, error) {
				q := url.Values{}
				if v := argString(a, "status"); v != "" {
					q.Set("status", v)
				}
				if v := argString(a, "device_id"); v != "" {
					q.Set("device_id", v)
				}
				if n := argInt(a, "limit"); n > 0 {
					q.Set("limit", fmt.Sprintf("%d", n))
				}
				return "GET", "/api/v1/alerts" + withQuery(q), nil, nil
			},
		},
		{
			def: tool("get_alert", "Get a single alert by ID.",
				map[string]any{"alert_id": strProp("Alert UUID.")}, []string{"alert_id"}),
			build: func(a map[string]any) (string, string, []byte, error) {
				id, err := argUUID(a, "alert_id")
				return "GET", "/api/v1/alerts/" + id, nil, err
			},
		},
		{
			def: tool("ack_alert", "Acknowledge an alert (requires alerts.manage).",
				map[string]any{"alert_id": strProp("Alert UUID.")}, []string{"alert_id"}),
			build: func(a map[string]any) (string, string, []byte, error) {
				id, err := argUUID(a, "alert_id")
				return "POST", "/api/v1/alerts/" + id + "/ack", nil, err
			},
		},
	}
}

func withQuery(q url.Values) string {
	if len(q) == 0 {
		return ""
	}
	return "?" + q.Encode()
}
