package api

import (
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/codex666-cenotaph/rmmagic/server/internal/auth"
	"github.com/codex666-cenotaph/rmmagic/server/internal/store"
)

// onlineWindow: a device is shown online if it has been seen within
// three heartbeat intervals.
const onlineWindow = 90 * time.Second

type deviceJSON struct {
	ID            uuid.UUID `json:"id"`
	SiteID        uuid.UUID `json:"site_id"`
	SiteName      string    `json:"site_name"`
	CustomerID    uuid.UUID `json:"customer_id"`
	CustomerName  string    `json:"customer_name"`
	Hostname      string    `json:"hostname"`
	OS            string    `json:"os"`
	Arch          string    `json:"arch"`
	AgentVersion  string    `json:"agent_version"`
	Status        string    `json:"status"`
	Tags          []string  `json:"tags"`
	UpdateChannel string    `json:"update_channel"`
	Online        bool      `json:"online"`
	// Health is the worst of the device's health checks: healthy /
	// warning / critical, or "unknown" when no check has reported.
	Health     string     `json:"health"`
	LastSeenAt *time.Time `json:"last_seen_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

func toDeviceJSON(d store.Device, health string) deviceJSON {
	online := d.Status == "active" && d.LastSeenAt != nil && time.Since(*d.LastSeenAt) < onlineWindow
	tags := d.Tags
	if tags == nil {
		tags = []string{}
	}
	if health == "" {
		health = store.HealthUnknown
	}
	return deviceJSON{
		ID: d.ID, SiteID: d.SiteID, SiteName: d.SiteName,
		CustomerID: d.CustomerID, CustomerName: d.CustomerName,
		Hostname: d.Hostname, OS: d.OS, Arch: d.Arch,
		AgentVersion: d.AgentVersion, Status: d.Status, Tags: tags,
		UpdateChannel: d.UpdateChannel, Online: online, Health: health,
		LastSeenAt: d.LastSeenAt, CreatedAt: d.CreatedAt,
	}
}

func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)

	out := []deviceJSON{}
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		devices, err := store.ListDevices(ctx, tx)
		if err != nil {
			return err
		}
		health, err := store.DeviceHealthMap(ctx, tx)
		if err != nil {
			return err
		}
		all := p.HasTenantWide(auth.PermDevicesRead)
		allowedCustomers := map[uuid.UUID]bool{}
		if !all {
			for _, id := range p.CustomerIDsWith(auth.PermDevicesRead) {
				allowedCustomers[id] = true
			}
		}
		for _, d := range devices {
			if all || allowedCustomers[d.CustomerID] {
				out = append(out, toDeviceJSON(d, health[d.ID]))
				continue
			}
			// Site-scoped grants.
			if requireInTx(ctx, tx, auth.PermDevicesRead, auth.Scope{Type: auth.ScopeSite, ID: d.SiteID}) == nil {
				out = append(out, toDeviceJSON(d, health[d.ID]))
			}
		}
		return nil
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": out})
}

// getAuthorizedDevice resolves the device under RLS and checks perm at
// its site scope (foreign/unknown IDs 404).
func getAuthorizedDevice(ctx *http.Request, tx pgx.Tx, perm auth.Permission, id uuid.UUID) (store.Device, error) {
	d, err := store.GetDevice(ctx.Context(), tx, id)
	if err != nil {
		return d, err
	}
	if err := requireInTx(ctx.Context(), tx, perm, auth.Scope{Type: auth.ScopeSite, ID: d.SiteID}); err != nil {
		return d, err
	}
	return d, nil
}

func (s *Server) handleGetDevice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var d store.Device
	var checks []store.DeviceHealthCheck
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		var err error
		d, err = getAuthorizedDevice(r, tx, auth.PermDevicesRead, id)
		if err != nil {
			return err
		}
		checks, err = store.ListDeviceHealth(ctx, tx, id)
		return err
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toDeviceJSON(d, worstHealth(checks)))
}

// worstHealth reduces a device's checks to a single overall status.
// ListDeviceHealth returns them worst-first, so the head wins; no checks
// means unknown.
func worstHealth(checks []store.DeviceHealthCheck) string {
	if len(checks) == 0 {
		return store.HealthUnknown
	}
	return checks[0].Status
}

// handleDeviceHealth returns the latest result of every health check that
// has run on the device.
func (s *Server) handleDeviceHealth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	checks := []store.DeviceHealthCheck{}
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		if _, err := getAuthorizedDevice(r, tx, auth.PermDevicesRead, id); err != nil {
			return err
		}
		c, err := store.ListDeviceHealth(ctx, tx, id)
		if err != nil {
			return err
		}
		if c != nil {
			checks = c
		}
		return nil
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"health": worstHealth(checks),
		"checks": checks,
	})
}

func (s *Server) handleDeviceStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	since := time.Now().Add(-time.Hour)
	until := time.Now()
	if v := r.URL.Query().Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "since must be RFC3339")
			return
		}
		since = t
	}
	if v := r.URL.Query().Get("until"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "until must be RFC3339")
			return
		}
		until = t
	}

	var samples []store.StatsSample
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		if _, err := getAuthorizedDevice(r, tx, auth.PermDevicesRead, id); err != nil {
			return err
		}
		var err error
		samples, err = store.ListStats(ctx, tx, id, since, until, 2000)
		return err
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if samples == nil {
		samples = []store.StatsSample{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"samples": samples})
}

const maxTagsPerDevice = 20

// tagPattern keeps tags to a predictable, URL/label-safe shape so they
// can be matched exactly by tag-scoped policies and shown in the UI.
var tagPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// normalizeTags lower-cases, trims, de-dupes and validates a tag list,
// returning the cleaned set (sorted, stable) or an error message.
func normalizeTags(in []string) ([]string, string) {
	seen := map[string]bool{}
	out := []string{}
	for _, raw := range in {
		t := strings.ToLower(strings.TrimSpace(raw))
		if t == "" {
			continue
		}
		if !tagPattern.MatchString(t) {
			return nil, "tag " + raw + " is invalid: use 1-32 chars of a-z, 0-9, - or _"
		}
		if seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	if len(out) > maxTagsPerDevice {
		return nil, "a device may carry at most 20 tags"
	}
	sort.Strings(out)
	return out, ""
}

func (s *Server) handleSetDeviceTags(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Tags []string `json:"tags"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	tags, msg := normalizeTags(req.Tags)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}

	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		if _, err := getAuthorizedDevice(r, tx, auth.PermDevicesManage, id); err != nil {
			return err
		}
		if err := store.SetDeviceTags(ctx, tx, id, tags); err != nil {
			return err
		}
		return recordAudit(ctx, tx, "device.tags", "device", id,
			map[string]any{"tags": tags})
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tags": tags})
}

func (s *Server) handleDecommissionDevice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		if _, err := getAuthorizedDevice(r, tx, auth.PermDevicesManage, id); err != nil {
			return err
		}
		if err := store.DecommissionDevice(ctx, tx, id); err != nil {
			return err
		}
		return recordAudit(ctx, tx, "device.decommission", "device", id, nil)
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	// Notify and disconnect a live agent after the revocation committed.
	if s.Gateway != nil {
		s.Gateway.Decommission(ctx, id)
	}
	writeJSON(w, http.StatusOK, struct{}{})
}
