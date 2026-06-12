package api

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/codex666-cenotaph/rmmagic/server/internal/auth"
	"github.com/codex666-cenotaph/rmmagic/server/internal/store"
	"github.com/codex666-cenotaph/rmmagic/shared/devicesig"
)

// Agent-facing endpoints. These are "public" in the route registry
// because no user session is involved; each performs its own device
// authentication (enrollment token, or Ed25519 request signature).

const (
	maxStatsBatch = 120 // samples per request
	// Stats uploads include the full service-state snapshot, so the
	// control-plane default (64 KiB) is too small.
	maxStatsBytes = 256 << 10
	// Inventory uploads carry the full package list; well above stats
	// payloads but still bounded.
	maxInventoryBytes = 2 << 20
	maxPackages       = 20000
	maxServices       = 2000
)

func (s *Server) handleAgentEnroll(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token        string `json:"token"`
		Hostname     string `json:"hostname"`
		OS           string `json:"os"`
		Arch         string `json:"arch"`
		AgentVersion string `json:"agent_version"`
		Pubkey       string `json:"pubkey"` // base64 raw Ed25519 public key
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	ip := clientIP(r)
	if !s.loginLimiter.Allow("enroll|" + ip) {
		writeError(w, http.StatusTooManyRequests, "too many attempts")
		return
	}
	req.Hostname = strings.TrimSpace(req.Hostname)
	if req.Token == "" || req.Hostname == "" || len(req.Hostname) > 253 {
		writeError(w, http.StatusBadRequest, "token and hostname required")
		return
	}
	pubkey, err := base64.StdEncoding.DecodeString(req.Pubkey)
	if err != nil || len(pubkey) != ed25519.PublicKeySize {
		writeError(w, http.StatusBadRequest, "pubkey must be a base64 Ed25519 public key")
		return
	}

	ctx := r.Context()
	var tok store.AuthEnrollmentToken
	err = s.Store.System(ctx, func(tx pgx.Tx) error {
		var err error
		tok, err = store.LookupEnrollmentToken(ctx, tx, auth.HashToken(req.Token))
		return err
	})
	if err != nil || tok.RevokedAt != nil || tok.ExpiresAt.Before(time.Now()) || tok.UseCount >= tok.MaxUses {
		writeError(w, http.StatusUnauthorized, "invalid enrollment token")
		return
	}

	var deviceID uuid.UUID
	err = s.Store.WithTenant(ctx, tok.TenantID, func(tx pgx.Tx) error {
		// Re-check usage atomically inside the transaction.
		if err := store.ConsumeEnrollmentToken(ctx, tx, tok.TokenID); err != nil {
			return err
		}
		var err error
		deviceID, err = store.CreateDevice(ctx, tx, tok.TenantID, tok.SiteID,
			req.Hostname, req.OS, req.Arch, req.AgentVersion)
		if err != nil {
			return err
		}
		if err := store.AddDeviceCredential(ctx, tx, tok.TenantID, deviceID,
			pubkey, devicesig.Fingerprint(pubkey)); err != nil {
			return err
		}
		return store.InsertAudit(ctx, tx, tok.TenantID, store.AuditEntry{
			ActorType: "device", ActorID: &deviceID, Action: "device.enroll",
			TargetType: strPtr("device"), TargetID: &deviceID, IP: &ip,
			Details: mustJSON(map[string]any{
				"hostname": req.Hostname, "os": req.OS, "site_id": tok.SiteID,
			}),
		})
	})
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid enrollment token")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"device_id": deviceID})
}

// authDeviceRequest verifies the Ed25519 signature headers on an
// agent-originated HTTP request and returns the verified identity.
func (s *Server) authDeviceRequest(r *http.Request, body []byte) (deviceID, tenantID uuid.UUID, err error) {
	deviceID, err = uuid.Parse(r.Header.Get("X-Device-Id"))
	if err != nil {
		return uuid.Nil, uuid.Nil, errors.New("bad device id")
	}
	ts, err := strconv.ParseInt(r.Header.Get("X-Timestamp"), 10, 64)
	if err != nil {
		return uuid.Nil, uuid.Nil, errors.New("bad timestamp")
	}
	if d := time.Since(time.Unix(ts, 0)); d > devicesig.MaxSkew || d < -devicesig.MaxSkew {
		return uuid.Nil, uuid.Nil, errors.New("timestamp outside skew window")
	}
	sig, err := base64.StdEncoding.DecodeString(r.Header.Get("X-Signature"))
	if err != nil {
		return uuid.Nil, uuid.Nil, errors.New("bad signature encoding")
	}

	var dev store.AuthDevice
	err = s.Store.System(r.Context(), func(tx pgx.Tx) error {
		var err error
		dev, err = store.LookupDevice(r.Context(), tx, deviceID)
		return err
	})
	if err != nil || dev.Status != "active" {
		return uuid.Nil, uuid.Nil, errors.New("unknown device")
	}
	if !devicesig.VerifyRequest(ed25519.PublicKey(dev.Pubkey), ts, body, sig) {
		return uuid.Nil, uuid.Nil, errors.New("bad signature")
	}
	return deviceID, dev.TenantID, nil
}

func (s *Server) handleAgentStats(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxStatsBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "body too large")
		return
	}
	deviceID, tenantID, err := s.authDeviceRequest(r, body)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "device authentication failed")
		return
	}

	var req struct {
		Samples []store.StatsSample `json:"samples"`
		// Optional snapshot of systemd service states, refreshed with
		// every upload so service-down policies evaluate fresh data.
		Services []store.ServiceState `json:"services,omitempty"`
	}
	if err := jsonUnmarshalStrict(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Samples) == 0 || len(req.Samples) > maxStatsBatch {
		writeError(w, http.StatusBadRequest, "samples must contain 1-120 entries")
		return
	}
	if len(req.Services) > maxServices {
		writeError(w, http.StatusBadRequest, "too many services")
		return
	}
	now := time.Now()
	for i := range req.Samples {
		// Clamp obviously bogus timestamps rather than polluting partitions.
		if req.Samples[i].TS.After(now.Add(time.Minute)) || req.Samples[i].TS.Before(now.Add(-24*time.Hour)) {
			writeError(w, http.StatusBadRequest, "sample timestamp out of range")
			return
		}
	}

	err = s.Store.WithTenant(r.Context(), tenantID, func(tx pgx.Tx) error {
		if err := store.InsertStats(r.Context(), tx, tenantID, deviceID, req.Samples); err != nil {
			return err
		}
		if req.Services != nil {
			return store.UpsertServices(r.Context(), tx, tenantID, deviceID, req.Services)
		}
		return nil
	})
	if err != nil {
		s.Log.Error("stats insert failed", "device_id", deviceID, "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusAccepted, struct{}{})
}

// handleAgentInventory ingests a full inventory upload (hardware,
// installed packages, service states), replacing the previous
// snapshot. Sent by agents on start, every 12h, and on the
// INVENTORY_REFRESH command.
func (s *Server) handleAgentInventory(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxInventoryBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "body too large")
		return
	}
	deviceID, tenantID, err := s.authDeviceRequest(r, body)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "device authentication failed")
		return
	}

	var req struct {
		CollectedAt time.Time               `json:"collected_at"`
		HW          *store.Hardware         `json:"hw"`
		Packages    []store.SoftwarePackage `json:"packages"`
		Services    []store.ServiceState    `json:"services,omitempty"`
	}
	if err := jsonUnmarshalStrict(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.HW == nil {
		writeError(w, http.StatusBadRequest, "hw is required")
		return
	}
	if len(req.Packages) > maxPackages || len(req.Services) > maxServices {
		writeError(w, http.StatusBadRequest, "inventory too large")
		return
	}
	collectedAt := req.CollectedAt
	now := time.Now()
	if collectedAt.IsZero() || collectedAt.After(now.Add(time.Minute)) || collectedAt.Before(now.Add(-24*time.Hour)) {
		collectedAt = now
	}

	err = s.Store.WithTenant(r.Context(), tenantID, func(tx pgx.Tx) error {
		if err := store.UpsertHardware(r.Context(), tx, tenantID, deviceID, *req.HW, collectedAt); err != nil {
			return err
		}
		if err := store.UpsertSoftware(r.Context(), tx, tenantID, deviceID, req.Packages, collectedAt); err != nil {
			return err
		}
		if req.Services != nil {
			return store.UpsertServices(r.Context(), tx, tenantID, deviceID, req.Services)
		}
		return nil
	})
	if err != nil {
		s.Log.Error("inventory upsert failed", "device_id", deviceID, "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusAccepted, struct{}{})
}

func strPtr(s string) *string { return &s }
