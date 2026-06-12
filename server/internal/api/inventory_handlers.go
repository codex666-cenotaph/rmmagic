package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/codex666-cenotaph/rmmagic/server/internal/auth"
	"github.com/codex666-cenotaph/rmmagic/server/internal/store"
)

func rawOr(v json.RawMessage, def string) json.RawMessage {
	if len(v) == 0 {
		return json.RawMessage(def)
	}
	return v
}

func (s *Server) handleGetInventory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var inv store.Inventory
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		if _, err := getAuthorizedDevice(r, tx, auth.PermDevicesRead, id); err != nil {
			return err
		}
		var err error
		inv, err = store.GetInventory(ctx, tx, id)
		return err
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"hw":                  rawOr(inv.HW, "null"),
		"hw_collected_at":     inv.HWCollectedAt,
		"packages":            rawOr(inv.Packages, "[]"),
		"sw_collected_at":     inv.SWCollectedAt,
		"services":            rawOr(inv.Services, "[]"),
		"services_updated_at": inv.ServicesUpdatedAt,
	})
}

// handleRefreshInventory asks a connected agent to re-collect now.
func (s *Server) handleRefreshInventory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		_, err := getAuthorizedDevice(r, tx, auth.PermDevicesManage, id)
		return err
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	sent := false
	if s.Gateway != nil {
		sent = s.Gateway.RequestInventoryRefresh(ctx, id)
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"requested": sent})
}

// handleEffectivePolicy returns the per-device result of policy
// inheritance resolution: which rule applies and from which policy.
func (s *Server) handleEffectivePolicy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var d store.Device
	var policies []store.Policy
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		var err error
		d, err = getAuthorizedDevice(r, tx, auth.PermPoliciesRead, id)
		if err != nil {
			return err
		}
		policies, err = store.ListPolicies(ctx, tx)
		return err
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}

	eff := effectiveRulesJSON(policies, store.PolicyDeviceScope{
		DeviceID: d.ID, SiteID: d.SiteID, CustomerID: d.CustomerID,
	})
	writeJSON(w, http.StatusOK, map[string]any{"rules": eff})
}

// effectiveRulesJSON mirrors the worker's merge for display: per rule
// type, the winning rule plus its source policy.
func effectiveRulesJSON(policies []store.Policy, scope store.PolicyDeviceScope) map[string]any {
	out := map[string]any{}
	type cand struct {
		rank   int
		policy store.Policy
		rules  map[string]json.RawMessage
	}
	var cands []cand
	for _, pol := range policies {
		if !pol.Enabled || !pol.AppliesTo(scope) {
			continue
		}
		var rules map[string]json.RawMessage
		if err := json.Unmarshal(pol.Rules, &rules); err != nil {
			continue
		}
		cands = append(cands, cand{rank: scopeRank(pol.ScopeType), policy: pol, rules: rules})
	}
	for rank := 0; rank <= 3; rank++ {
		for _, c := range cands {
			if c.rank != rank {
				continue
			}
			for ruleType, rule := range c.rules {
				out[ruleType] = map[string]any{
					"rule":        rule,
					"policy_id":   c.policy.ID,
					"policy_name": c.policy.Name,
					"scope_type":  c.policy.ScopeType,
				}
			}
		}
	}
	return out
}

func scopeRank(scopeType string) int {
	switch scopeType {
	case "tenant":
		return 0
	case "customer":
		return 1
	case "site":
		return 2
	case "device":
		return 3
	}
	return -1
}

// unused placeholder to keep time import if trimmed later
var _ = time.Time{}
