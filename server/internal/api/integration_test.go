package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pquerna/otp/totp"

	"github.com/codex666-cenotaph/rmmagic/server/internal/bootstrap"
	"github.com/codex666-cenotaph/rmmagic/server/internal/secrets"
	"github.com/codex666-cenotaph/rmmagic/server/internal/store"
)

// Integration suite: runs the full API against real Postgres, with the
// pool downgraded to the rmm_app role so RLS is live. The cross-tenant
// probes here are the tenant-isolation guardrail required by the plan.
// Skipped unless RMM_TEST_DATABASE_URL is set (see `make test-integration`).

func TestAPIIntegration(t *testing.T) {
	dsn := os.Getenv("RMM_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("RMM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()

	// Reset schema and apply migrations on a privileged connection.
	priv, err := store.Open(ctx, dsn, "")
	if err != nil {
		t.Fatal(err)
	}
	defer priv.Close()
	applyMigrations(t, ctx, priv)

	// Bootstrap two tenants.
	tenantA, err := bootstrap.Run(ctx, priv, bootstrap.Input{
		TenantName: "MSP Alpha", Slug: "alpha", Email: "owner@alpha.test", Password: "alpha-owner-pass-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bootstrap.Run(ctx, priv, bootstrap.Input{
		TenantName: "MSP Beta", Slug: "beta", Email: "owner@beta.test", Password: "beta-owner-pass-12"}); err != nil {
		t.Fatal(err)
	}
	_ = tenantA

	// App store under the RLS-constrained role.
	appStore, err := store.Open(ctx, dsn, "rmm_app")
	if err != nil {
		t.Fatal(err)
	}
	defer appStore.Close()

	box, err := secrets.NewBox(strings.Repeat("0badc0de", 8))
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(appStore, box, slog.New(slog.NewTextHandler(io.Discard, nil)), false)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	alpha := newClient(t, ts.URL)
	beta := newClient(t, ts.URL)

	// --- login and org CRUD as tenant A owner ---
	alpha.post(t, "/api/v1/auth/login", obj{"email": "owner@alpha.test", "password": "alpha-owner-pass-1"}, 200)
	me := alpha.get(t, "/api/v1/auth/me", 200)
	if me["tenant"].(map[string]any)["slug"] != "alpha" {
		t.Fatalf("wrong tenant in /me: %v", me["tenant"])
	}

	cust := alpha.post(t, "/api/v1/customers", obj{"name": "Acme Corp"}, 201)
	custID := cust["id"].(string)
	site := alpha.post(t, "/api/v1/customers/"+custID+"/sites", obj{"name": "HQ", "timezone": "Europe/Amsterdam"}, 201)
	siteID := site["id"].(string)

	list := alpha.get(t, "/api/v1/customers", 200)
	if n := len(list["customers"].([]any)); n != 1 {
		t.Fatalf("alpha should see 1 customer, got %d", n)
	}

	// --- cross-tenant isolation probes as tenant B owner ---
	beta.post(t, "/api/v1/auth/login", obj{"email": "owner@beta.test", "password": "beta-owner-pass-12"}, 200)
	if n := len(beta.get(t, "/api/v1/customers", 200)["customers"].([]any)); n != 0 {
		t.Fatalf("beta must see 0 customers, got %d", n)
	}
	// Direct object references across tenants must 404, not 403.
	beta.req(t, "PATCH", "/api/v1/customers/"+custID, obj{"name": "pwned"}, 404)
	beta.req(t, "DELETE", "/api/v1/customers/"+custID, nil, 404)
	beta.get(t, "/api/v1/customers/"+custID+"/sites", 404)
	beta.post(t, "/api/v1/customers/"+custID+"/sites", obj{"name": "planted"}, 404)
	beta.req(t, "PATCH", "/api/v1/sites/"+siteID, obj{"name": "pwned"}, 404)
	beta.req(t, "DELETE", "/api/v1/sites/"+siteID, nil, 404)

	// And the name must be unchanged for A.
	got := alpha.get(t, "/api/v1/customers", 200)
	if name := got["customers"].([]any)[0].(map[string]any)["name"]; name != "Acme Corp" {
		t.Fatalf("customer name mutated cross-tenant: %v", name)
	}

	// --- scoped RBAC: customer-scoped technician ---
	other := alpha.post(t, "/api/v1/customers", obj{"name": "Other Co"}, 201)
	tech := alpha.post(t, "/api/v1/users", obj{"email": "tech@alpha.test", "password": "tech-password-123"}, 201)
	techID := tech["id"].(string)

	roles := alpha.get(t, "/api/v1/roles", 200)
	var techRoleID string
	for _, r := range roles["roles"].([]any) {
		if r.(map[string]any)["name"] == "Technician" {
			techRoleID = r.(map[string]any)["id"].(string)
		}
	}
	alpha.post(t, "/api/v1/users/"+techID+"/assignments",
		obj{"role_id": techRoleID, "scope_type": "customer", "scope_id": custID}, 201)

	techClient := newClient(t, ts.URL)
	techClient.post(t, "/api/v1/auth/login", obj{"email": "tech@alpha.test", "password": "tech-password-123"}, 200)
	visible := techClient.get(t, "/api/v1/customers", 200)["customers"].([]any)
	if len(visible) != 1 || visible[0].(map[string]any)["id"] != custID {
		t.Fatalf("scoped tech should see exactly the assigned customer, got %v", visible)
	}
	// Technician cannot touch the other customer or tenant-level user management.
	techClient.get(t, "/api/v1/customers/"+other["id"].(string)+"/sites", 404)
	techClient.post(t, "/api/v1/users", obj{"email": "x@alpha.test", "password": "irrelevant-pass-1"}, 403)
	// Technician lacks org.manage entirely → coarse middleware 403.
	techClient.post(t, "/api/v1/customers", obj{"name": "Nope"}, 403)

	// --- API tokens ---
	tok := alpha.post(t, "/api/v1/api-tokens",
		obj{"name": "ci", "permissions": []string{"org.read"}}, 201)
	plaintext := tok["token"].(string)
	bearer := newClient(t, ts.URL)
	bearer.authHeader = "Bearer " + plaintext
	if n := len(bearer.get(t, "/api/v1/customers", 200)["customers"].([]any)); n != 2 {
		t.Fatalf("token should list alpha's 2 customers, got %d", n)
	}
	bearer.post(t, "/api/v1/customers", obj{"name": "Nope"}, 403) // org.read only
	alpha.req(t, "DELETE", "/api/v1/api-tokens/"+tok["id"].(string), nil, 204)
	bearer.get(t, "/api/v1/customers", 401) // revoked

	// --- MFA: enroll, then full login flow ---
	setup := alpha.post(t, "/api/v1/auth/mfa/setup", obj{}, 200)
	secret := setup["secret"].(string)
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	enabled := alpha.post(t, "/api/v1/auth/mfa/enable", obj{"code": code}, 200)
	if n := len(enabled["recovery_codes"].([]any)); n != 8 {
		t.Fatalf("expected 8 recovery codes, got %d", n)
	}

	relogin := newClient(t, ts.URL)
	resp := relogin.post(t, "/api/v1/auth/login", obj{"email": "owner@alpha.test", "password": "alpha-owner-pass-1"}, 200)
	if resp["mfa_required"] != true {
		t.Fatal("expected mfa_required after enrollment")
	}
	relogin.get(t, "/api/v1/auth/me", 401) // pending session is locked out
	code2, _ := totp.GenerateCode(secret, time.Now())
	relogin.post(t, "/api/v1/auth/mfa/verify", obj{"code": code2}, 200)
	relogin.get(t, "/api/v1/auth/me", 200)

	// --- audit trail exists and is tenant-local ---
	entries := alpha.get(t, "/api/v1/audit?limit=200", 200)["entries"].([]any)
	actions := map[string]bool{}
	for _, e := range entries {
		actions[e.(map[string]any)["action"].(string)] = true
	}
	for _, want := range []string{"user.login", "customer.create", "site.create",
		"api_token.create", "api_token.revoke", "user.mfa_enabled", "role_assignment.create"} {
		if !actions[want] {
			t.Errorf("audit log missing action %s", want)
		}
	}
	betaEntries := beta.get(t, "/api/v1/audit?limit=200", 200)["entries"].([]any)
	for _, e := range betaEntries {
		if a := e.(map[string]any)["action"]; a == "customer.create" {
			t.Fatal("beta's audit log contains alpha's entries")
		}
	}

	// --- bad login is rejected and unknown email does not differ in status ---
	newClient(t, ts.URL).post(t, "/api/v1/auth/login", obj{"email": "owner@alpha.test", "password": "wrong-password-x"}, 401)
	newClient(t, ts.URL).post(t, "/api/v1/auth/login", obj{"email": "ghost@nowhere.test", "password": "wrong-password-x"}, 401)
}

func applyMigrations(t *testing.T, ctx context.Context, priv *store.Store) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "..", "migrations", "*.up.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no migrations found: %v", err)
	}
	sort.Strings(files)
	err = priv.System(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public; GRANT ALL ON SCHEMA public TO PUBLIC"); err != nil {
			return err
		}
		for _, f := range files {
			sql, err := os.ReadFile(f)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, string(sql)); err != nil {
				return fmt.Errorf("%s: %w", f, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// --- tiny test client ---

type obj = map[string]any

type client struct {
	base       string
	http       *http.Client
	authHeader string
}

func newClient(t *testing.T, base string) *client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &client{base: base, http: &http.Client{Jar: jar}}
}

func (c *client) req(t *testing.T, method, path string, body any, wantStatus int) obj {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.authHeader != "" {
		req.Header.Set("Authorization", c.authHeader)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s %s: got %d want %d (body: %s)", method, path, resp.StatusCode, wantStatus, raw)
	}
	out := obj{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

func (c *client) get(t *testing.T, path string, want int) obj {
	t.Helper()
	return c.req(t, "GET", path, nil, want)
}

func (c *client) post(t *testing.T, path string, body any, want int) obj {
	t.Helper()
	return c.req(t, "POST", path, body, want)
}
