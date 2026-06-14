package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5"
	"github.com/pquerna/otp/totp"
	"google.golang.org/protobuf/proto"

	"github.com/codex666-cenotaph/rmmagic/server/internal/bootstrap"
	"github.com/codex666-cenotaph/rmmagic/server/internal/gateway"
	"github.com/codex666-cenotaph/rmmagic/server/internal/secrets"
	"github.com/codex666-cenotaph/rmmagic/server/internal/storage"
	"github.com/codex666-cenotaph/rmmagic/server/internal/store"
	"github.com/codex666-cenotaph/rmmagic/server/internal/worker"
	"github.com/codex666-cenotaph/rmmagic/shared/devicesig"
	rmmpb "github.com/codex666-cenotaph/rmmagic/shared/rmmpb/rmm/v1"
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
	gw := gateway.New(appStore, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv.Gateway = gw
	blobs, err := storage.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv.Blobs = blobs
	mux := http.NewServeMux()
	mux.Handle("/api/v1/", srv.Handler())
	mux.Handle("/agent/v1/enroll", srv.Handler())
	mux.Handle("/agent/v1/stats", srv.Handler())
	mux.Handle("/agent/v1/releases/", srv.Handler())
	mux.HandleFunc("GET /agent/v1/connect", gw.HandleConnect)
	ts := httptest.NewServer(mux)
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

	testDeviceFlow(t, ts.URL, alpha, beta, siteID)
	testScriptsFlow(t, ts.URL, alpha, beta, siteID)
	testTargetsAndSchedulesFlow(t, ts.URL, alpha, beta, siteID, srv, appStore, priv)
	testAppsAndUpdatesFlow(t, ts.URL, alpha, beta, siteID)
}

// testAppsAndUpdatesFlow exercises M6: package (apt/dnf) deploy jobs and the
// signed auto-update rollout (offer over the channel, status back, device
// version advance), plus update-channel changes and tenant isolation.
func testAppsAndUpdatesFlow(t *testing.T, baseURL string, alpha, beta *client, siteID string) {
	ctx := context.Background()

	// Enroll and connect a Linux device.
	tok := alpha.post(t, "/api/v1/enrollment-tokens", obj{"site_id": siteID}, 201)
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	enrolled := newClient(t, baseURL).post(t, "/agent/v1/enroll", obj{
		"token": tok["token"].(string), "hostname": "app-box", "os": "linux", "arch": "amd64",
		"agent_version": "0.0.0-test", "pubkey": base64.StdEncoding.EncodeToString(pub),
	}, 201)
	deviceID := enrolled["device_id"].(string)

	wsURL := strings.Replace(baseURL, "http://", "ws://", 1) + "/agent/v1/connect"
	ws, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close(websocket.StatusNormalClosure, "")
	ch := wsRead(t, ctx, ws)
	wsWrite(t, ctx, ws, &rmmpb.Envelope{Payload: &rmmpb.Envelope_AuthResponse{AuthResponse: &rmmpb.AuthResponse{
		DeviceId: deviceID, Signature: devicesig.SignChallenge(priv, ch.GetAuthChallenge().GetNonce())}}})
	wsWrite(t, ctx, ws, &rmmpb.Envelope{Payload: &rmmpb.Envelope_Hello{Hello: &rmmpb.Hello{
		ProtocolVersion: 1, AgentVersion: "0.0.0-test", Os: "linux", Arch: "amd64", Hostname: "app-box"}}})
	if wsRead(t, ctx, ws).GetHelloAck() == nil {
		t.Fatal("expected hello ack")
	}

	// --- App deploy: a package install job uses the same dispatch path ---
	job := alpha.post(t, "/api/v1/apps/deploy", obj{
		"device_id": deviceID, "operation": "install", "packages": []string{"nginx", "curl"},
	}, 201)
	jobID := job["job_id"].(string)

	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	var cmd *rmmpb.CommandRequest
	for cmd == nil {
		env, err := readEnvelope(rctx, ws)
		if err != nil {
			t.Fatalf("no package CommandRequest: %v", err)
		}
		cmd = env.GetCommandRequest()
	}
	cancel()
	if cmd.Kind != rmmpb.CommandKind_COMMAND_KIND_PACKAGE_INSTALL {
		t.Fatalf("expected PACKAGE_INSTALL kind, got %v", cmd.Kind)
	}
	if !strings.Contains(string(cmd.Spec), "nginx") {
		t.Fatalf("package spec missing nginx: %s", cmd.Spec)
	}
	wsWrite(t, ctx, ws, &rmmpb.Envelope{MessageId: "pkg-res", Payload: &rmmpb.Envelope_CommandResult{
		CommandResult: &rmmpb.CommandResult{CommandId: cmd.CommandId,
			Status: rmmpb.CommandStatus_COMMAND_STATUS_SUCCEEDED, Output: []byte("installed nginx")}}})

	deadline := time.Now().Add(5 * time.Second)
	var jobStatus string
	for time.Now().Before(deadline) {
		jobStatus = alpha.get(t, "/api/v1/jobs/"+jobID, 200)["status"].(string)
		if jobStatus == "succeeded" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if jobStatus != "succeeded" {
		t.Fatalf("package job should be succeeded, got %q", jobStatus)
	}

	// Validation: bad operation and shell-unsafe package names are rejected.
	alpha.post(t, "/api/v1/apps/deploy", obj{"device_id": deviceID, "operation": "frob", "packages": []string{"x"}}, 400)
	alpha.post(t, "/api/v1/apps/deploy", obj{"device_id": deviceID, "operation": "install", "packages": []string{"bad; rm -rf /"}}, 400)

	// --- Signed, server-hosted release: register metadata, upload binary,
	// roll out, download over device-authed endpoint ---
	binary := []byte("pretend new agent binary bytes")
	_, relPriv, _ := ed25519.GenerateKey(nil)
	sum := sha256.Sum256(binary)
	shaHex := hex.EncodeToString(sum[:])
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(relPriv, binary))

	// Server-hosted release: metadata first, no external URL.
	created := alpha.post(t, "/api/v1/agent-releases", obj{
		"channel": "stable", "version": "9.9.9", "os": "linux", "arch": "amd64",
		"sha256": shaHex, "signature": sig, "notes": "test release",
	}, 201)
	relID := created["id"].(string)

	// Bad sha256 is rejected at registration.
	alpha.post(t, "/api/v1/agent-releases", obj{
		"channel": "stable", "version": "9.9.10", "os": "linux", "arch": "amd64",
		"sha256": "nothex", "signature": sig,
	}, 400)

	// Rollout before the binary is uploaded is refused.
	alpha.post(t, "/api/v1/agent-releases/"+relID+"/rollout", obj{"device_id": deviceID}, 400)

	// Upload: wrong bytes (sha mismatch) → 400; correct bytes → 200.
	alpha.postFile(t, "/api/v1/agent-releases/"+relID+"/binary", []byte("WRONG BYTES"), 400)
	alpha.postFile(t, "/api/v1/agent-releases/"+relID+"/binary", binary, 200)

	// Now the catalog reports a binary is present.
	hasBinary := false
	for _, r := range alpha.get(t, "/api/v1/agent-releases?channel=stable", 200)["releases"].([]any) {
		m := r.(map[string]any)
		if m["version"] == "9.9.9" && m["has_binary"] == true {
			hasBinary = true
		}
	}
	if !hasBinary {
		t.Fatal("release should report has_binary after upload")
	}

	res := alpha.post(t, "/api/v1/agent-releases/"+relID+"/rollout", obj{"device_id": deviceID}, 200)
	if res["matched"].(float64) != 1 {
		t.Fatalf("expected 1 matched device, got %v", res["matched"])
	}

	// The agent receives a signed UpdateOffer whose URL is the device-authed
	// server download path.
	octx, cancel2 := context.WithTimeout(ctx, 5*time.Second)
	var offer *rmmpb.UpdateOffer
	for offer == nil {
		env, err := readEnvelope(octx, ws)
		if err != nil {
			t.Fatalf("no UpdateOffer: %v", err)
		}
		offer = env.GetUpdateOffer()
	}
	cancel2()
	dlPath := "/agent/v1/releases/" + relID + "/download"
	if offer.Version != "9.9.9" || offer.Sha256 != shaHex || offer.Url != dlPath {
		t.Fatalf("unexpected offer: version=%q sha=%q url=%q", offer.Version, offer.Sha256, offer.Url)
	}

	// Device-signed GET returns the exact bytes; unauthenticated GET is 401.
	tsig := time.Now().Unix()
	dreq, _ := http.NewRequest("GET", baseURL+dlPath, nil)
	dreq.Header.Set("X-Device-Id", deviceID)
	dreq.Header.Set("X-Timestamp", strconv.FormatInt(tsig, 10))
	dreq.Header.Set("X-Signature", base64.StdEncoding.EncodeToString(devicesig.SignRequest(priv, tsig, []byte(dlPath))))
	dresp, err := http.DefaultClient.Do(dreq)
	if err != nil {
		t.Fatal(err)
	}
	dbody, _ := io.ReadAll(dresp.Body)
	dresp.Body.Close()
	if dresp.StatusCode != 200 || !bytes.Equal(dbody, binary) {
		t.Fatalf("signed download failed: status=%d bytes=%q", dresp.StatusCode, dbody)
	}
	uresp, err := http.Get(baseURL + dlPath)
	if err != nil {
		t.Fatal(err)
	}
	uresp.Body.Close()
	if uresp.StatusCode != 401 {
		t.Fatalf("unauthenticated download should be 401, got %d", uresp.StatusCode)
	}

	// Agent reports the update applied; server advances the device version.
	wsWrite(t, ctx, ws, &rmmpb.Envelope{MessageId: "upd-1", Payload: &rmmpb.Envelope_UpdateStatus{
		UpdateStatus: &rmmpb.UpdateStatus{Version: "9.9.9", Phase: rmmpb.UpdatePhase_UPDATE_PHASE_APPLIED}}})

	deadline = time.Now().Add(5 * time.Second)
	applied := false
	for time.Now().Before(deadline) && !applied {
		for _, u := range alpha.get(t, "/api/v1/device-updates", 200)["updates"].([]any) {
			m := u.(map[string]any)
			if m["device_id"] == deviceID && m["phase"] == "applied" {
				applied = true
			}
		}
		if !applied {
			time.Sleep(50 * time.Millisecond)
		}
	}
	if !applied {
		t.Fatal("device update phase did not reach applied")
	}
	if v := alpha.get(t, "/api/v1/devices/"+deviceID, 200)["agent_version"]; v != "9.9.9" {
		t.Fatalf("agent_version not advanced after update: %v", v)
	}

	// --- update channel change ---
	alpha.post(t, "/api/v1/devices/"+deviceID+"/update-channel", obj{"channel": "beta"}, 200)
	if c := alpha.get(t, "/api/v1/devices/"+deviceID, 200)["update_channel"]; c != "beta" {
		t.Fatalf("update_channel not changed: %v", c)
	}

	// --- tenant isolation: device_updates are tenant-scoped; rollout and
	// channel changes against another tenant's device are blocked. ---
	if n := len(beta.get(t, "/api/v1/device-updates", 200)["updates"].([]any)); n != 0 {
		t.Fatalf("beta must see 0 device-updates, got %d", n)
	}
	beta.post(t, "/api/v1/agent-releases/"+relID+"/rollout", obj{"device_id": deviceID}, 400)
	beta.post(t, "/api/v1/devices/"+deviceID+"/update-channel", obj{"channel": "beta"}, 404)
}

// testDeviceFlow exercises M2: enrollment, gateway auth, heartbeat,
// stats ingest, decommission, and the cross-tenant device probes.
func testDeviceFlow(t *testing.T, baseURL string, alpha, beta *client, siteID string) {
	ctx := context.Background()

	// Enrollment token (1 use, by design below).
	tok := alpha.post(t, "/api/v1/enrollment-tokens", obj{"site_id": siteID}, 201)
	enrollToken := tok["token"].(string)
	if !strings.HasPrefix(enrollToken, "rmme_") {
		t.Fatalf("unexpected token format: %s", enrollToken)
	}

	// Enroll a fake agent.
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	anon := newClient(t, baseURL)
	enrolled := anon.post(t, "/agent/v1/enroll", obj{
		"token": enrollToken, "hostname": "test-box", "os": "linux", "arch": "amd64",
		"agent_version": "0.0.0-test", "pubkey": base64.StdEncoding.EncodeToString(pub),
	}, 201)
	deviceID := enrolled["device_id"].(string)

	// Single-use token: second enrollment must fail.
	anon.post(t, "/agent/v1/enroll", obj{
		"token": enrollToken, "hostname": "test-box-2", "os": "linux", "arch": "amd64",
		"agent_version": "0.0.0-test", "pubkey": base64.StdEncoding.EncodeToString(pub),
	}, 401)

	// Connect to the gateway with challenge-response auth.
	wsURL := strings.Replace(baseURL, "http://", "ws://", 1) + "/agent/v1/connect"
	ws, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close(websocket.StatusNormalClosure, "")

	env := wsRead(t, ctx, ws)
	nonce := env.GetAuthChallenge().GetNonce()
	if len(nonce) != 32 {
		t.Fatalf("expected 32-byte nonce, got %d", len(nonce))
	}
	wsWrite(t, ctx, ws, &rmmpb.Envelope{Payload: &rmmpb.Envelope_AuthResponse{
		AuthResponse: &rmmpb.AuthResponse{DeviceId: deviceID, Signature: devicesig.SignChallenge(priv, nonce)},
	}})
	wsWrite(t, ctx, ws, &rmmpb.Envelope{Payload: &rmmpb.Envelope_Hello{Hello: &rmmpb.Hello{
		ProtocolVersion: 1, AgentVersion: "0.0.0-test", Os: "linux", Arch: "amd64", Hostname: "test-box",
	}}})
	if wsRead(t, ctx, ws).GetHelloAck() == nil {
		t.Fatal("expected hello ack")
	}

	// Device is online and visible to alpha.
	devs := alpha.get(t, "/api/v1/devices", 200)["devices"].([]any)
	if len(devs) != 1 {
		t.Fatalf("alpha should see 1 device, got %d", len(devs))
	}
	dev := devs[0].(map[string]any)
	if dev["online"] != true || dev["hostname"] != "test-box" {
		t.Fatalf("device not online after hello: %v", dev)
	}

	// Cross-tenant: beta sees nothing and cannot act on the device.
	if n := len(beta.get(t, "/api/v1/devices", 200)["devices"].([]any)); n != 0 {
		t.Fatalf("beta must see 0 devices, got %d", n)
	}
	beta.get(t, "/api/v1/devices/"+deviceID, 404)
	beta.post(t, "/api/v1/devices/"+deviceID+"/decommission", nil, 404)
	beta.post(t, "/api/v1/enrollment-tokens", obj{"site_id": siteID}, 404)

	// Signed stats ingest.
	statsBody, _ := json.Marshal(obj{"samples": []obj{{
		"ts": time.Now().UTC().Format(time.RFC3339), "cpu_pct": 12.5,
		"mem_used": 1024, "mem_total": 2048,
		"disks": []obj{{"mount": "/", "used": 1, "total": 10}},
		"net":   obj{"rx_bytes": 1, "tx_bytes": 2},
	}}})
	unix := time.Now().Unix()
	doSigned := func(sig []byte, wantStatus int) {
		req, _ := http.NewRequest("POST", baseURL+"/agent/v1/stats", bytes.NewReader(statsBody))
		req.Header.Set("X-Device-Id", deviceID)
		req.Header.Set("X-Timestamp", strconv.FormatInt(unix, 10))
		req.Header.Set("X-Signature", base64.StdEncoding.EncodeToString(sig))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != wantStatus {
			t.Fatalf("stats post: got %d want %d", resp.StatusCode, wantStatus)
		}
	}
	doSigned(devicesig.SignRequest(priv, unix, statsBody), 202)
	// Wrong key must be rejected.
	_, otherKey, _ := ed25519.GenerateKey(nil)
	doSigned(devicesig.SignRequest(otherKey, unix, statsBody), 401)

	samples := alpha.get(t, "/api/v1/devices/"+deviceID+"/stats", 200)["samples"].([]any)
	if len(samples) != 1 {
		t.Fatalf("expected 1 stats sample, got %d", len(samples))
	}

	// Decommission: live connection receives Decommission and re-auth fails.
	alpha.post(t, "/api/v1/devices/"+deviceID+"/decommission", nil, 200)
	deadline := time.Now().Add(5 * time.Second)
	gotDecommission := false
	for time.Now().Before(deadline) {
		rctx, cancel := context.WithTimeout(ctx, time.Second)
		env, err := readEnvelope(rctx, ws)
		cancel()
		if err != nil {
			break // connection closed by server, acceptable terminal state
		}
		if env.GetDecommission() != nil {
			gotDecommission = true
			break
		}
	}
	if !gotDecommission {
		t.Error("agent never received Decommission frame")
	}

	ws2, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws2.Close(websocket.StatusNormalClosure, "")
	env = wsRead(t, ctx, ws2)
	wsWrite(t, ctx, ws2, &rmmpb.Envelope{Payload: &rmmpb.Envelope_AuthResponse{
		AuthResponse: &rmmpb.AuthResponse{
			DeviceId:  deviceID,
			Signature: devicesig.SignChallenge(priv, env.GetAuthChallenge().GetNonce()),
		},
	}})
	rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	_, err = readEnvelope(rctx, ws2)
	cancel()
	if err == nil {
		t.Fatal("decommissioned device must not authenticate")
	}
	// Stats from a revoked device are rejected too.
	doSigned(devicesig.SignRequest(priv, unix, statsBody), 401)

	// Token revocation.
	tok2 := alpha.post(t, "/api/v1/enrollment-tokens", obj{"site_id": siteID, "max_uses": 5}, 201)
	alpha.req(t, "DELETE", "/api/v1/enrollment-tokens/"+tok2["id"].(string), nil, 204)
	anon.post(t, "/agent/v1/enroll", obj{
		"token": tok2["token"].(string), "hostname": "x", "os": "linux", "arch": "amd64",
		"agent_version": "t", "pubkey": base64.StdEncoding.EncodeToString(pub),
	}, 401)
}

// testScriptsFlow exercises M3: script CRUD, job dispatch (online and
// offline), CommandResult ingestion, output retrieval, and cross-tenant
// isolation of scripts and jobs.
func testScriptsFlow(t *testing.T, baseURL string, alpha, beta *client, siteID string) {
	t.Helper()
	ctx := context.Background()

	// --- Script CRUD ---
	sc := alpha.post(t, "/api/v1/scripts", obj{
		"name": "Hello Script", "description": "prints hello",
		"language": "bash", "body": "#!/bin/bash\necho hello",
		"parameters": []any{},
	}, 201)
	scriptID := sc["id"].(string)

	list := alpha.get(t, "/api/v1/scripts", 200)["scripts"].([]any)
	if len(list) != 1 {
		t.Fatalf("expected 1 script, got %d", len(list))
	}
	alpha.req(t, "PATCH", "/api/v1/scripts/"+scriptID, obj{
		"name": "Hello Script v2", "description": "prints hello",
		"language": "bash", "body": "#!/bin/bash\necho hello v2",
		"parameters": []any{},
	}, 200)

	// Cross-tenant: beta cannot see alpha's scripts.
	if n := len(beta.get(t, "/api/v1/scripts", 200)["scripts"].([]any)); n != 0 {
		t.Fatalf("beta must see 0 scripts, got %d", n)
	}
	beta.get(t, "/api/v1/scripts/"+scriptID, 404)

	// --- Enroll a fresh device for job testing ---
	tok := alpha.post(t, "/api/v1/enrollment-tokens", obj{"site_id": siteID}, 201)
	enrollToken := tok["token"].(string)
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	anon := newClient(t, baseURL)
	enrolled := anon.post(t, "/agent/v1/enroll", obj{
		"token": enrollToken, "hostname": "job-box", "os": "linux", "arch": "amd64",
		"agent_version": "0.0.0-test", "pubkey": base64.StdEncoding.EncodeToString(pub),
	}, 201)
	deviceID := enrolled["device_id"].(string)

	// Connect to gateway.
	wsURL := strings.Replace(baseURL, "http://", "ws://", 1) + "/agent/v1/connect"
	connectAndAuth := func() *websocket.Conn {
		ws, _, err := websocket.Dial(ctx, wsURL, nil)
		if err != nil {
			t.Fatal(err)
		}
		env := wsRead(t, ctx, ws)
		wsWrite(t, ctx, ws, &rmmpb.Envelope{Payload: &rmmpb.Envelope_AuthResponse{
			AuthResponse: &rmmpb.AuthResponse{DeviceId: deviceID, Signature: devicesig.SignChallenge(priv, env.GetAuthChallenge().GetNonce())},
		}})
		wsWrite(t, ctx, ws, &rmmpb.Envelope{Payload: &rmmpb.Envelope_Hello{Hello: &rmmpb.Hello{
			ProtocolVersion: 1, AgentVersion: "0.0.0-test", Os: "linux", Arch: "amd64", Hostname: "job-box",
		}}})
		if wsRead(t, ctx, ws).GetHelloAck() == nil {
			t.Fatal("expected hello ack")
		}
		return ws
	}
	ws := connectAndAuth()
	defer ws.Close(websocket.StatusNormalClosure, "")

	// --- Online dispatch: device is connected, command arrives immediately ---
	job := alpha.post(t, "/api/v1/scripts/"+scriptID+"/dispatch",
		obj{"device_id": deviceID, "timeout_s": 30, "parameters": obj{}}, 201)
	jobID := job["job_id"].(string)

	// Read the CommandRequest from the WS.
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	var cmdReq *rmmpb.CommandRequest
	for cmdReq == nil {
		env, err := readEnvelope(rctx, ws)
		if err != nil {
			t.Fatalf("no CommandRequest received: %v", err)
		}
		cmdReq = env.GetCommandRequest()
	}
	cancel()
	if cmdReq.CommandId == "" {
		t.Fatal("CommandRequest has no command_id")
	}
	if cmdReq.Kind != rmmpb.CommandKind_COMMAND_KIND_SCRIPT {
		t.Fatalf("expected SCRIPT kind, got %v", cmdReq.Kind)
	}

	// Send back a successful CommandResult.
	wsWrite(t, ctx, ws, &rmmpb.Envelope{
		MessageId: "result-1",
		Payload: &rmmpb.Envelope_CommandResult{CommandResult: &rmmpb.CommandResult{
			CommandId: cmdReq.CommandId,
			Status:    rmmpb.CommandStatus_COMMAND_STATUS_SUCCEEDED,
			ExitCode:  0,
			Output:    []byte("hello v2\n"),
		}},
	})

	// Poll until the job is succeeded (gateway processes the result async).
	deadline := time.Now().Add(5 * time.Second)
	var jobStatus string
	for time.Now().Before(deadline) {
		j := alpha.get(t, "/api/v1/jobs/"+jobID, 200)
		jobStatus = j["status"].(string)
		if jobStatus == "succeeded" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if jobStatus != "succeeded" {
		t.Fatalf("job should be succeeded, got %q", jobStatus)
	}

	// Retrieve output.
	out := alpha.get(t, "/api/v1/jobs/"+jobID+"/output", 200)
	if out["output"] != "hello v2\n" {
		t.Fatalf("unexpected output: %v", out["output"])
	}

	// Cross-tenant: beta cannot see the job.
	if n := len(beta.get(t, "/api/v1/jobs", 200)["jobs"].([]any)); n != 0 {
		t.Fatalf("beta must see 0 jobs, got %d", n)
	}
	beta.get(t, "/api/v1/jobs/"+jobID, 404)

	// Intra-tenant scope: a technician scoped to a *different* customer
	// must not see jobs for this device, even on the unfiltered list.
	// (Guards the scope filter in handleListJobs.)
	otherCust := alpha.post(t, "/api/v1/customers", obj{"name": "Scope Probe Co"}, 201)
	scopedTech := alpha.post(t, "/api/v1/users",
		obj{"email": "scoped-tech@alpha.test", "password": "scoped-tech-pass-1"}, 201)
	var techRoleID string
	for _, rr := range alpha.get(t, "/api/v1/roles", 200)["roles"].([]any) {
		if rr.(map[string]any)["name"] == "Technician" {
			techRoleID = rr.(map[string]any)["id"].(string)
		}
	}
	alpha.post(t, "/api/v1/users/"+scopedTech["id"].(string)+"/assignments",
		obj{"role_id": techRoleID, "scope_type": "customer", "scope_id": otherCust["id"]}, 201)
	scopedClient := newClient(t, baseURL)
	scopedClient.post(t, "/api/v1/auth/login",
		obj{"email": "scoped-tech@alpha.test", "password": "scoped-tech-pass-1"}, 200)
	if n := len(scopedClient.get(t, "/api/v1/jobs", 200)["jobs"].([]any)); n != 0 {
		t.Fatalf("out-of-scope technician must see 0 jobs on the unfiltered list, got %d", n)
	}
	// And cannot read the specific job either.
	scopedClient.get(t, "/api/v1/jobs/"+jobID, 404)

	// --- Offline queue: create job while device is disconnected ---
	ws.Close(websocket.StatusNormalClosure, "")
	time.Sleep(100 * time.Millisecond) // let the server detect disconnect

	offlineJob := alpha.post(t, "/api/v1/scripts/"+scriptID+"/dispatch",
		obj{"device_id": deviceID, "timeout_s": 30, "parameters": obj{}}, 201)
	offlineJobID := offlineJob["job_id"].(string)
	j2 := alpha.get(t, "/api/v1/jobs/"+offlineJobID, 200)
	if s := j2["status"].(string); s != "pending" {
		t.Fatalf("offline job should be pending, got %q", s)
	}

	// Reconnect — the gateway should drain the pending job.
	ws2 := connectAndAuth()
	defer ws2.Close(websocket.StatusNormalClosure, "")

	rctx2, cancel2 := context.WithTimeout(ctx, 5*time.Second)
	var cmdReq2 *rmmpb.CommandRequest
	for cmdReq2 == nil {
		env, err := readEnvelope(rctx2, ws2)
		if err != nil {
			t.Fatalf("no CommandRequest on reconnect: %v", err)
		}
		cmdReq2 = env.GetCommandRequest()
	}
	cancel2()

	// Send back the result.
	wsWrite(t, ctx, ws2, &rmmpb.Envelope{
		Payload: &rmmpb.Envelope_CommandResult{CommandResult: &rmmpb.CommandResult{
			CommandId: cmdReq2.CommandId,
			Status:    rmmpb.CommandStatus_COMMAND_STATUS_FAILED,
			ExitCode:  1,
			Output:    []byte("error: something failed\n"),
		}},
	})

	deadline2 := time.Now().Add(5 * time.Second)
	var offlineStatus string
	for time.Now().Before(deadline2) {
		j := alpha.get(t, "/api/v1/jobs/"+offlineJobID, 200)
		offlineStatus = j["status"].(string)
		if offlineStatus == "failed" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if offlineStatus != "failed" {
		t.Fatalf("offline job should be failed, got %q", offlineStatus)
	}

	// --- Archive script: excluded from the default list, still
	// retrievable by ID, and no longer dispatchable. ---
	alpha.req(t, "DELETE", "/api/v1/scripts/"+scriptID, nil, 200)
	if n := len(alpha.get(t, "/api/v1/scripts", 200)["scripts"].([]any)); n != 0 {
		t.Fatalf("archived script must not appear in default list, got %d", n)
	}
	detail := alpha.get(t, "/api/v1/scripts/"+scriptID, 200)
	if detail["archived"] != true {
		t.Fatalf("script should be marked archived, got %v", detail["archived"])
	}
	archived := alpha.get(t, "/api/v1/scripts?archived=true", 200)["scripts"].([]any)
	if len(archived) != 1 {
		t.Fatalf("archived list should have 1 entry, got %d", len(archived))
	}
	alpha.post(t, "/api/v1/scripts/"+scriptID+"/dispatch",
		obj{"device_id": deviceID, "timeout_s": 30}, 404)
}

// testTargetsAndSchedulesFlow exercises the M3 completion surface:
// site/customer target selectors, the blast-radius confirmation flow,
// queued-job expiry, and cron schedules fired by the worker.
func testTargetsAndSchedulesFlow(t *testing.T, baseURL string, alpha, beta *client, siteID string,
	srv *Server, appStore, priv *store.Store) {
	t.Helper()
	ctx := context.Background()

	sc := alpha.post(t, "/api/v1/scripts", obj{
		"name": "Targeted Script", "language": "bash", "body": "echo hi", "parameters": []any{},
	}, 201)
	scriptID := sc["id"].(string)

	// Two more enrolled (offline) devices in the site; job-box from the
	// scripts flow is still active, so the site now has 3 active devices.
	enrollDevice := func(hostname string) string {
		tok := alpha.post(t, "/api/v1/enrollment-tokens", obj{"site_id": siteID}, 201)
		pub, _, err := ed25519.GenerateKey(nil)
		if err != nil {
			t.Fatal(err)
		}
		resp := newClient(t, baseURL).post(t, "/agent/v1/enroll", obj{
			"token": tok["token"].(string), "hostname": hostname, "os": "linux", "arch": "amd64",
			"agent_version": "0.0.0-test", "pubkey": base64.StdEncoding.EncodeToString(pub),
		}, 201)
		return resp["device_id"].(string)
	}
	enrollDevice("target-box-1")
	enrollDevice("target-box-2")

	// --- blast radius: site-wide dispatch above the threshold needs a
	// confirmation token that encodes the resolved device count ---
	srv.BlastRadius = 1
	conflict := alpha.post(t, "/api/v1/scripts/"+scriptID+"/dispatch",
		obj{"target": obj{"site_id": siteID}}, 409)
	if conflict["confirmation_required"] != true {
		t.Fatalf("expected confirmation_required, got %v", conflict)
	}
	count := int(conflict["device_count"].(float64))
	if count != 3 {
		t.Fatalf("expected device_count 3, got %d", count)
	}
	// A garbage token is rejected (still 409).
	alpha.post(t, "/api/v1/scripts/"+scriptID+"/dispatch",
		obj{"target": obj{"site_id": siteID}, "confirm_token": "bogus"}, 409)
	// The issued token authorizes exactly this dispatch.
	created := alpha.post(t, "/api/v1/scripts/"+scriptID+"/dispatch",
		obj{"target": obj{"site_id": siteID}, "confirm_token": conflict["confirm_token"]}, 201)
	if n := len(created["job_ids"].([]any)); n != 3 {
		t.Fatalf("expected 3 jobs from site dispatch, got %d", n)
	}
	srv.BlastRadius = 25

	// Cross-tenant: beta dispatching against alpha's site resolves to
	// nothing under RLS.
	betaScript := beta.post(t, "/api/v1/scripts", obj{
		"name": "Beta Script", "language": "bash", "body": "echo beta", "parameters": []any{},
	}, 201)
	beta.post(t, "/api/v1/scripts/"+betaScript["id"].(string)+"/dispatch",
		obj{"target": obj{"site_id": siteID}}, 400)

	// --- expiry: a queued job past its window is swept to 'expired' and
	// never (re-)delivered ---
	expiring := alpha.post(t, "/api/v1/scripts/"+scriptID+"/dispatch",
		obj{"target": obj{"site_id": siteID}, "expires_in_s": 1}, 201)
	expiringID := expiring["job_ids"].([]any)[0].(string)
	time.Sleep(1100 * time.Millisecond)

	wk := worker.New(appStore, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	wk.Tick(ctx)

	if got := alpha.get(t, "/api/v1/jobs/"+expiringID, 200)["status"]; got != "expired" {
		t.Fatalf("queued job past expiry should be expired, got %v", got)
	}

	// --- schedules ---
	alpha.post(t, "/api/v1/schedules", obj{
		"script_id": scriptID, "name": "bad cron", "cron": "not a cron",
		"target": obj{"site_id": siteID},
	}, 400)

	schedule := alpha.post(t, "/api/v1/schedules", obj{
		"script_id": scriptID, "name": "nightly", "cron": "0 3 * * *",
		"target": obj{"site_id": siteID}, "parameters": obj{}, "timeout_s": 60,
	}, 201)
	scheduleID := schedule["id"].(string)

	// Schedule creation above the blast radius needs the same ack.
	srv.BlastRadius = 1
	schedConflict := alpha.post(t, "/api/v1/schedules", obj{
		"script_id": scriptID, "name": "big nightly", "cron": "@daily",
		"target": obj{"site_id": siteID},
	}, 409)
	confirmed := alpha.post(t, "/api/v1/schedules", obj{
		"script_id": scriptID, "name": "big nightly", "cron": "@daily",
		"target": obj{"site_id": siteID}, "confirm_token": schedConflict["confirm_token"],
	}, 201)
	srv.BlastRadius = 25
	alpha.req(t, "DELETE", "/api/v1/schedules/"+confirmed["id"].(string), nil, 204)

	// Cross-tenant: beta cannot see or modify alpha's schedule.
	if n := len(beta.get(t, "/api/v1/schedules", 200)["schedules"].([]any)); n != 0 {
		t.Fatalf("beta must see 0 schedules, got %d", n)
	}
	beta.get(t, "/api/v1/schedules/"+scheduleID, 404)
	beta.req(t, "DELETE", "/api/v1/schedules/"+scheduleID, nil, 404)

	// --- worker fires a due schedule exactly once ---
	// Backdate next_run_at on a privileged connection (tests only).
	err := priv.System(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			"UPDATE schedules SET next_run_at = now() - interval '1 minute' WHERE id = $1", scheduleID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	wk.Tick(ctx)

	jobs := alpha.get(t, "/api/v1/jobs?limit=200", 200)["jobs"].([]any)
	fired := 0
	for _, j := range jobs {
		if j.(map[string]any)["schedule_id"] == scheduleID {
			fired++
		}
	}
	if fired != 3 {
		t.Fatalf("schedule should have fired 1 job per active site device (3), got %d", fired)
	}

	// next_run_at advanced past now, so a second tick must not re-fire.
	detail := alpha.get(t, "/api/v1/schedules/"+scheduleID, 200)
	next, err := time.Parse(time.RFC3339, detail["next_run_at"].(string))
	if err != nil || !next.After(time.Now()) {
		t.Fatalf("next_run_at not advanced: %v (%v)", detail["next_run_at"], err)
	}
	if detail["last_run_at"] == nil {
		t.Fatal("last_run_at not recorded")
	}
	wk.Tick(ctx)
	jobs = alpha.get(t, "/api/v1/jobs?limit=200", 200)["jobs"].([]any)
	refired := 0
	for _, j := range jobs {
		if j.(map[string]any)["schedule_id"] == scheduleID {
			refired++
		}
	}
	if refired != fired {
		t.Fatalf("second tick re-fired the schedule: %d -> %d jobs", fired, refired)
	}

	// The firing is audited as a system action.
	foundAudit := false
	for _, e := range alpha.get(t, "/api/v1/audit?limit=200", 200)["entries"].([]any) {
		entry := e.(map[string]any)
		if entry["action"] == "schedule.run" && entry["actor_type"] == "system" {
			foundAudit = true
		}
	}
	if !foundAudit {
		t.Error("schedule.run audit entry missing")
	}

	alpha.req(t, "DELETE", "/api/v1/schedules/"+scheduleID, nil, 204)
}

func readEnvelope(ctx context.Context, ws *websocket.Conn) (*rmmpb.Envelope, error) {
	_, b, err := ws.Read(ctx)
	if err != nil {
		return nil, err
	}
	env := &rmmpb.Envelope{}
	if err := proto.Unmarshal(b, env); err != nil {
		return nil, err
	}
	return env, nil
}

func wsRead(t *testing.T, ctx context.Context, ws *websocket.Conn) *rmmpb.Envelope {
	t.Helper()
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	env, err := readEnvelope(rctx, ws)
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func wsWrite(t *testing.T, ctx context.Context, ws *websocket.Conn, env *rmmpb.Envelope) {
	t.Helper()
	b, err := proto.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.Write(ctx, websocket.MessageBinary, b); err != nil {
		t.Fatal(err)
	}
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

// postFile uploads data as a multipart "file" field.
func (c *client) postFile(t *testing.T, path string, data []byte, want int) obj {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "rmmagent")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatal(err)
	}
	mw.Close()
	req, err := http.NewRequest("POST", c.base+path, &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		t.Fatalf("POST %s: got %d want %d (body: %s)", path, resp.StatusCode, want, raw)
	}
	out := obj{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

func (c *client) post(t *testing.T, path string, body any, want int) obj {
	t.Helper()
	return c.req(t, "POST", path, body, want)
}
