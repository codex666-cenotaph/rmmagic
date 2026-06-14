package api

import (
	"net/http"
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
	ID           uuid.UUID  `json:"id"`
	SiteID       uuid.UUID  `json:"site_id"`
	SiteName     string     `json:"site_name"`
	CustomerID   uuid.UUID  `json:"customer_id"`
	CustomerName string     `json:"customer_name"`
	Hostname     string     `json:"hostname"`
	OS           string     `json:"os"`
	Arch         string     `json:"arch"`
	AgentVersion  string     `json:"agent_version"`
	Status        string     `json:"status"`
	UpdateChannel string     `json:"update_channel"`
	Online        bool       `json:"online"`
	LastSeenAt    *time.Time `json:"last_seen_at"`
	CreatedAt     time.Time  `json:"created_at"`
}

func toDeviceJSON(d store.Device) deviceJSON {
	online := d.Status == "active" && d.LastSeenAt != nil && time.Since(*d.LastSeenAt) < onlineWindow
	return deviceJSON{
		ID: d.ID, SiteID: d.SiteID, SiteName: d.SiteName,
		CustomerID: d.CustomerID, CustomerName: d.CustomerName,
		Hostname: d.Hostname, OS: d.OS, Arch: d.Arch,
		AgentVersion: d.AgentVersion, Status: d.Status, UpdateChannel: d.UpdateChannel, Online: online,
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
		all := p.HasTenantWide(auth.PermDevicesRead)
		allowedCustomers := map[uuid.UUID]bool{}
		if !all {
			for _, id := range p.CustomerIDsWith(auth.PermDevicesRead) {
				allowedCustomers[id] = true
			}
		}
		for _, d := range devices {
			if all || allowedCustomers[d.CustomerID] {
				out = append(out, toDeviceJSON(d))
				continue
			}
			// Site-scoped grants.
			if requireInTx(ctx, tx, auth.PermDevicesRead, auth.Scope{Type: auth.ScopeSite, ID: d.SiteID}) == nil {
				out = append(out, toDeviceJSON(d))
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
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		var err error
		d, err = getAuthorizedDevice(r, tx, auth.PermDevicesRead, id)
		return err
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toDeviceJSON(d))
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
