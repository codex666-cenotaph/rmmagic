package api

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/codex666-cenotaph/rmmagic/server/internal/auth"
	"github.com/codex666-cenotaph/rmmagic/server/internal/store"
)

// Mass-action safeguard: dispatches and schedules targeting more than
// BlastRadius devices require a server-issued confirmation token that
// encodes the resolved device count, so the caller has seen the real
// blast radius before anything runs.
const defaultBlastRadius = 25

const (
	confirmTokenTTL = 5 * time.Minute
	// AD context for secrets.Box so confirm tokens can't be confused
	// with other sealed values.
	confirmTokenContext = "dispatch-confirm"
)

// resolveAuthorizedTarget expands the target selector and checks
// PermScriptsExecute on every site it touches. Any out-of-scope device
// fails the whole request: explicitly targeting devices you cannot
// execute on is an error, not something to silently filter.
func resolveAuthorizedTarget(ctx context.Context, tx pgx.Tx, target store.JobTarget) ([]store.TargetDevice, error) {
	devices, err := store.ResolveTarget(ctx, tx, target)
	if err != nil {
		return nil, err
	}
	checkedSites := map[uuid.UUID]bool{}
	for _, d := range devices {
		if checkedSites[d.SiteID] {
			continue
		}
		if err := requireInTx(ctx, tx, auth.PermScriptsExecute, auth.Scope{Type: auth.ScopeSite, ID: d.SiteID}); err != nil {
			return nil, err
		}
		checkedSites[d.SiteID] = true
	}
	return devices, nil
}

type confirmClaims struct {
	ScriptID    uuid.UUID `json:"script_id"`
	TargetHash  string    `json:"target_hash"`
	DeviceCount int       `json:"device_count"`
	Exp         int64     `json:"exp"`
}

func targetHash(target store.JobTarget) string {
	b, _ := json.Marshal(target)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (s *Server) confirmAD(tenantID uuid.UUID) []byte {
	return append([]byte(confirmTokenContext), tenantID[:]...)
}

// issueConfirmToken seals the blast-radius facts into a short-lived
// stateless token bound to the tenant, script, and exact target.
func (s *Server) issueConfirmToken(tenantID, scriptID uuid.UUID, target store.JobTarget, count int) (string, error) {
	claims, _ := json.Marshal(confirmClaims{
		ScriptID:    scriptID,
		TargetHash:  targetHash(target),
		DeviceCount: count,
		Exp:         time.Now().Add(confirmTokenTTL).Unix(),
	})
	sealed, err := s.Box.Seal(claims, s.confirmAD(tenantID))
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

// checkConfirmToken reports whether token authorizes running scriptID
// against exactly this target at this device count.
func (s *Server) checkConfirmToken(token string, tenantID, scriptID uuid.UUID, target store.JobTarget, count int) bool {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return false
	}
	plain, err := s.Box.Open(raw, s.confirmAD(tenantID))
	if err != nil {
		return false
	}
	var c confirmClaims
	if err := json.Unmarshal(plain, &c); err != nil {
		return false
	}
	return c.ScriptID == scriptID &&
		c.TargetHash == targetHash(target) &&
		c.DeviceCount == count &&
		time.Now().Unix() <= c.Exp
}

// requireBlastRadiusAck enforces the mass-action safeguard. When
// confirmation is needed and missing it writes the 409 response with a
// fresh token and returns false; the caller must stop.
func (s *Server) requireBlastRadiusAck(w http.ResponseWriter,
	tenantID, scriptID uuid.UUID, target store.JobTarget, count int, confirmToken string) bool {
	if count <= s.BlastRadius {
		return true
	}
	if confirmToken != "" && s.checkConfirmToken(confirmToken, tenantID, scriptID, target, count) {
		return true
	}
	token, err := s.issueConfirmToken(tenantID, scriptID, target, count)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return false
	}
	writeJSON(w, http.StatusConflict, map[string]any{
		"confirmation_required": true,
		"device_count":          count,
		"confirm_token":         token,
	})
	return false
}

// errNoTargetDevices distinguishes an empty resolution from store errors
// so handlers can return 400 instead of 404/500.
var errNoTargetDevices = errors.New("target resolves to no active devices")
