package api

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/codex666-cenotaph/rmmagic/server/internal/auth"
	"github.com/codex666-cenotaph/rmmagic/server/internal/store"
)

// maxReleaseUpload caps a binary upload; matches the agent's download cap.
const maxReleaseUpload = 256 << 20 // 256 MiB

var validChannels = map[string]bool{"stable": true, "beta": true}

// errReleaseNoBinary is returned when rolling out a release that has neither
// an uploaded binary nor an external URL.
var errReleaseNoBinary = errors.New("release has no binary uploaded yet")

type releaseJSON struct {
	ID        uuid.UUID `json:"id"`
	Channel   string    `json:"channel"`
	Version   string    `json:"version"`
	OS        string    `json:"os"`
	Arch      string    `json:"arch"`
	URL       string    `json:"url,omitempty"`
	HasBinary bool      `json:"has_binary"`
	SHA256    string    `json:"sha256"`
	Signature string    `json:"signature"`
	SizeBytes int64     `json:"size_bytes"`
	Notes     string    `json:"notes"`
	CreatedAt time.Time `json:"created_at"`
}

func toReleaseJSON(r store.AgentRelease) releaseJSON {
	return releaseJSON{
		ID: r.ID, Channel: r.Channel, Version: r.Version, OS: r.OS, Arch: r.Arch,
		URL: r.URL, HasBinary: r.StorageKey != "" || r.URL != "",
		SHA256: r.SHA256, Signature: r.Signature, SizeBytes: r.SizeBytes,
		Notes: r.Notes, CreatedAt: r.CreatedAt,
	}
}

func (s *Server) handleListReleases(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	channel := r.URL.Query().Get("channel")
	if channel != "" && !validChannels[channel] {
		writeError(w, http.StatusBadRequest, "invalid channel")
		return
	}
	var releases []store.AgentRelease
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		var err error
		releases, err = store.ListReleases(ctx, tx, channel)
		return err
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out := make([]releaseJSON, 0, len(releases))
	for _, rel := range releases {
		out = append(out, toReleaseJSON(rel))
	}
	writeJSON(w, http.StatusOK, map[string]any{"releases": out})
}

type createReleaseReq struct {
	Channel   string `json:"channel"`
	Version   string `json:"version"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	URL       string `json:"url"`
	SHA256    string `json:"sha256"`
	Signature string `json:"signature"`
	SizeBytes int64  `json:"size_bytes"`
	Notes     string `json:"notes"`
}

func (s *Server) handleCreateRelease(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	var req createReleaseReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Channel == "" {
		req.Channel = "stable"
	}
	if !validChannels[req.Channel] {
		writeError(w, http.StatusBadRequest, "channel must be stable or beta")
		return
	}
	if strings.TrimSpace(req.Version) == "" || strings.TrimSpace(req.OS) == "" || strings.TrimSpace(req.Arch) == "" {
		writeError(w, http.StatusBadRequest, "version, os, and arch are required")
		return
	}
	// url is optional: server-hosted releases upload the binary afterwards.
	// When present it must be http(s).
	if req.URL != "" && !strings.HasPrefix(req.URL, "https://") && !strings.HasPrefix(req.URL, "http://") {
		writeError(w, http.StatusBadRequest, "url must be http(s)")
		return
	}
	if _, err := hex.DecodeString(req.SHA256); err != nil || len(req.SHA256) != 64 {
		writeError(w, http.StatusBadRequest, "sha256 must be 64 hex chars")
		return
	}
	if _, err := base64.StdEncoding.DecodeString(req.Signature); err != nil || req.Signature == "" {
		writeError(w, http.StatusBadRequest, "signature must be base64")
		return
	}

	rel := store.AgentRelease{
		Channel: req.Channel, Version: req.Version, OS: req.OS, Arch: req.Arch,
		URL: req.URL, SHA256: strings.ToLower(req.SHA256), Signature: req.Signature,
		SizeBytes: req.SizeBytes, Notes: req.Notes, CreatedBy: &p.UserID,
	}
	var id uuid.UUID
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		var err error
		id, err = store.CreateRelease(ctx, tx, rel)
		if err != nil {
			return err
		}
		return recordAudit(ctx, tx, "agent_release.create", "agent_release", id,
			map[string]any{"version": rel.Version, "channel": rel.Channel, "os": rel.OS, "arch": rel.Arch})
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

// handleUploadReleaseBinary stores the actual binary for a release in the
// server's blob storage so agents can fetch it over a device-authenticated
// endpoint (no dependency on a public/auth-walled artifact host). The
// uploaded bytes' sha256 must match the value registered with the release;
// the server cannot verify the Ed25519 signature (no public key
// server-side) — that remains the agent's check before swapping.
func (s *Server) handleUploadReleaseBinary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	if s.Blobs == nil {
		writeError(w, http.StatusServiceUnavailable, "release storage not configured")
		return
	}

	var rel store.AgentRelease
	if err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		var err error
		rel, err = store.GetRelease(ctx, tx, id)
		return err
	}); err != nil {
		writeStoreError(w, err)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxReleaseUpload+(1<<20))
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid or oversized upload")
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing file field")
		return
	}
	defer file.Close()

	// First pass: hash (the multipart file is seekable — spooled to disk
	// above the in-memory threshold), then rewind and stream to storage.
	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		writeError(w, http.StatusBadRequest, "read upload failed")
		return
	}
	if sum := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(sum, rel.SHA256) {
		writeError(w, http.StatusBadRequest, "uploaded file sha256 does not match the registered release")
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	key := "releases/" + id.String()
	if err := s.Blobs.Put(ctx, key, file, hdr.Size); err != nil {
		s.Log.Error("release blob write failed", "release_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	err = s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		if err := store.SetReleaseStorageKey(ctx, tx, id, key, hdr.Size); err != nil {
			return err
		}
		return recordAudit(ctx, tx, "agent_release.upload", "agent_release", id,
			map[string]any{"version": rel.Version, "size_bytes": hdr.Size})
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"size_bytes": hdr.Size})
}

type rolloutReq struct {
	DeviceID     uuid.UUID       `json:"device_id"`
	Target       store.JobTarget `json:"target"`
	ConfirmToken string          `json:"confirm_token"`
}

// handleRolloutRelease offers a release to every targeted device whose
// os/arch matches it, recording the rollout and pushing an UpdateOffer to
// the ones currently online. Same blast-radius safeguard as dispatch.
func (s *Server) handleRolloutRelease(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	releaseID, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var req rolloutReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.DeviceID != uuid.Nil {
		req.Target = store.JobTarget{DeviceIDs: []uuid.UUID{req.DeviceID}}
	}
	if err := req.Target.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var rel store.AgentRelease
	var matched []store.Device
	confirmed := true
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		var err error
		rel, err = store.GetRelease(ctx, tx, releaseID)
		if err != nil {
			return err
		}
		if rel.StorageKey == "" && rel.URL == "" {
			return errReleaseNoBinary
		}
		devices, err := resolveAuthorizedTarget(ctx, tx, req.Target, auth.PermAgentUpdate)
		if err != nil {
			return err
		}
		if len(devices) == 0 {
			return errNoTargetDevices
		}
		if !s.requireBlastRadiusAck(w, p.TenantID, releaseID, req.Target, len(devices), req.ConfirmToken) {
			confirmed = false
			return nil
		}
		// Only offer to devices whose platform matches the release.
		for _, d := range devices {
			dev, err := store.GetDevice(ctx, tx, d.ID)
			if err != nil {
				return err
			}
			if dev.OS != rel.OS || dev.Arch != rel.Arch {
				continue
			}
			if err := store.OfferDeviceUpdate(ctx, tx, p.TenantID, dev.ID, rel.Version, &p.UserID); err != nil {
				return err
			}
			matched = append(matched, dev)
		}
		return recordAudit(ctx, tx, "agent_release.rollout", "agent_release", releaseID,
			map[string]any{"version": rel.Version, "target": req.Target,
				"device_count": len(devices), "matched": len(matched)})
	})
	if errors.Is(err, errNoTargetDevices) {
		writeError(w, http.StatusBadRequest, errNoTargetDevices.Error())
		return
	}
	if errors.Is(err, errReleaseNoBinary) {
		writeError(w, http.StatusBadRequest, errReleaseNoBinary.Error())
		return
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !confirmed {
		return
	}

	// Push offers to the devices currently connected to this gateway;
	// offline devices pick the offer up when their rollout state is re-sent.
	online := 0
	if s.Gateway != nil {
		for _, dev := range matched {
			if s.Gateway.OfferUpdate(ctx, dev.ID, rel) {
				online++
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version":        rel.Version,
		"matched":        len(matched),
		"online_offered": online,
	})
}

func (s *Server) handleListDeviceUpdates(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	var updates []store.DeviceUpdate
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		var err error
		updates, err = store.ListDeviceUpdates(ctx, tx)
		return err
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	type duJSON struct {
		DeviceID  uuid.UUID `json:"device_id"`
		Version   string    `json:"version"`
		Phase     string    `json:"phase"`
		Error     string    `json:"error,omitempty"`
		OfferedAt time.Time `json:"offered_at"`
		UpdatedAt time.Time `json:"updated_at"`
	}
	out := make([]duJSON, 0, len(updates))
	for _, u := range updates {
		out = append(out, duJSON{DeviceID: u.DeviceID, Version: u.Version, Phase: u.Phase,
			Error: u.Error, OfferedAt: u.OfferedAt, UpdatedAt: u.UpdatedAt})
	}
	writeJSON(w, http.StatusOK, map[string]any{"updates": out})
}

type updateChannelReq struct {
	Channel string `json:"channel"`
}

func (s *Server) handleSetDeviceUpdateChannel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var req updateChannelReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if !validChannels[req.Channel] {
		writeError(w, http.StatusBadRequest, "channel must be stable or beta")
		return
	}
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		if _, err := getAuthorizedDevice(r, tx, auth.PermDevicesManage, id); err != nil {
			return err
		}
		if err := store.SetDeviceUpdateChannel(ctx, tx, id, req.Channel); err != nil {
			return err
		}
		return recordAudit(ctx, tx, "device.update_channel", "device", id,
			map[string]any{"channel": req.Channel})
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}
