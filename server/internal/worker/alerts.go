package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/codex666-cenotaph/rmmagic/server/internal/alerts"
	"github.com/codex666-cenotaph/rmmagic/server/internal/store"
)

const (
	// statsWindow is the lookback for threshold evaluation (CPU is
	// averaged over it, memory/disk use the latest sample in it).
	statsWindow = 10 * time.Minute
	// servicesFreshFor bounds how old a service-state snapshot may be
	// and still be evaluated.
	servicesFreshFor = 5 * time.Minute
	// deliveryBatch is the max notifications sent per tenant per tick.
	deliveryBatch = 50
)

// evaluateAlerts runs one policy-evaluation pass for a tenant: resolve
// inheritance per device, fire/resolve alerts, and enqueue
// notifications — all in one transaction so alert state and its outbox
// entries commit together.
func (w *Worker) evaluateAlerts(ctx context.Context, tenantID uuid.UUID) error {
	now := time.Now()
	return w.Store.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		if n, err := store.ResolveAlertsForDecommissioned(ctx, tx); err != nil {
			return err
		} else if n > 0 {
			w.Log.Info("resolved alerts on decommissioned devices", "tenant_id", tenantID, "count", n)
		}

		policies, err := store.ListPolicies(ctx, tx)
		if err != nil {
			return err
		}
		devices, err := store.CollectEvalDevices(ctx, tx, statsWindow)
		if err != nil {
			return err
		}

		for _, dev := range devices {
			eff := effectiveFor(w.Log, policies, dev)
			state := evalState(now, dev)
			findings, evaluated := alerts.Evaluate(now, eff, state)

			firing, err := store.ListFiringAlertsForDevice(ctx, tx, dev.DeviceID)
			if err != nil {
				return err
			}
			if len(findings) == 0 && len(firing) == 0 {
				continue
			}

			firingKeys := map[string]bool{}
			for _, a := range firing {
				firingKeys[a.DedupKey] = true
			}
			openKeys := map[string]bool{}
			for _, f := range findings {
				openKeys[f.DedupKey] = true
				if firingKeys[f.DedupKey] {
					continue
				}
				details, _ := json.Marshal(f.Details)
				policyID := f.PolicyID
				alertID, inserted, err := store.OpenAlert(ctx, tx, tenantID, dev.DeviceID, &policyID,
					f.RuleType, f.DedupKey, string(f.Severity), f.Message, details, f.ChannelIDs)
				if err != nil {
					return err
				}
				if inserted {
					if err := store.EnqueueDeliveries(ctx, tx, tenantID, alertID, f.ChannelIDs, "fired"); err != nil {
						return err
					}
					w.Log.Info("alert fired", "tenant_id", tenantID, "device_id", dev.DeviceID,
						"rule", f.RuleType, "message", f.Message)
				}
			}

			evaluatedSet := map[string]bool{}
			for _, rt := range evaluated {
				evaluatedSet[rt] = true
			}
			for _, a := range firing {
				if openKeys[a.DedupKey] {
					continue
				}
				switch {
				case !ruleConfigured(eff, a.RuleType):
					// The rule was removed; the condition can no longer be
					// tracked, so close the alert without claiming it cleared.
					if err := store.ResolveAlert(ctx, tx, a.ID); err != nil {
						return err
					}
					w.Log.Info("alert resolved (rule removed)", "tenant_id", tenantID, "alert_id", a.ID)
				case evaluatedSet[a.RuleType]:
					if err := store.ResolveAlert(ctx, tx, a.ID); err != nil {
						return err
					}
					if err := store.EnqueueDeliveries(ctx, tx, tenantID, a.ID, a.ChannelIDs, "resolved"); err != nil {
						return err
					}
					w.Log.Info("alert resolved", "tenant_id", tenantID, "alert_id", a.ID, "rule", a.RuleType)
				}
				// Rule configured but not evaluated (stale data): leave open.
			}
		}
		return nil
	})
}

// effectiveFor filters the tenant's policies down to the device and
// merges them. Policies with unparseable rules are skipped (they were
// validated at write time; this guards old rows).
func effectiveFor(log interface{ Error(string, ...any) }, policies []store.Policy, dev store.EvalDevice) alerts.Effective {
	scope := store.PolicyDeviceScope{DeviceID: dev.DeviceID, SiteID: dev.SiteID, CustomerID: dev.CustomerID}
	var scoped []alerts.ScopedPolicy
	for _, p := range policies {
		if !p.Enabled || !p.AppliesTo(scope) {
			continue
		}
		var rules alerts.Rules
		if err := json.Unmarshal(p.Rules, &rules); err != nil {
			log.Error("policy has invalid rules; skipping", "policy_id", p.ID, "error", err)
			continue
		}
		scoped = append(scoped, alerts.ScopedPolicy{
			ID:         p.ID,
			ScopeRank:  alerts.ScopeRank(p.ScopeType),
			Rules:      rules,
			ChannelIDs: p.ChannelIDs,
		})
	}
	return alerts.Merge(scoped)
}

func ruleConfigured(eff alerts.Effective, ruleType string) bool {
	switch ruleType {
	case alerts.RuleCPUPct:
		return eff.CPUPct != nil
	case alerts.RuleMemPct:
		return eff.MemPct != nil
	case alerts.RuleDiskPct:
		return eff.DiskPct != nil
	case alerts.RuleOffline:
		return eff.Offline != nil
	case alerts.RuleServiceDown:
		return eff.ServiceDown != nil
	}
	return false
}

// evalState converts the store row into the evaluator's input,
// decoding the jsonb blobs.
func evalState(now time.Time, dev store.EvalDevice) alerts.DeviceState {
	st := alerts.DeviceState{
		DeviceID:       dev.DeviceID,
		Hostname:       dev.Hostname,
		LastSeenAt:     dev.LastSeenAt,
		HasRecentStats: dev.HasRecentStats,
		AvgCPUPct:      dev.AvgCPUPct,
		MemUsedPct:     dev.MemUsedPct,
	}
	if len(dev.Disks) > 0 {
		var disks []struct {
			Mount string `json:"mount"`
			Used  int64  `json:"used"`
			Total int64  `json:"total"`
		}
		if err := json.Unmarshal(dev.Disks, &disks); err == nil {
			for _, d := range disks {
				st.Disks = append(st.Disks, alerts.DiskUsage{Mount: d.Mount, Used: d.Used, Total: d.Total})
			}
		}
	}
	if dev.ServicesUpdatedAt != nil && now.Sub(*dev.ServicesUpdatedAt) <= servicesFreshFor && len(dev.Services) > 0 {
		var services []store.ServiceState
		if err := json.Unmarshal(dev.Services, &services); err == nil {
			st.Services = map[string]string{}
			for _, s := range services {
				st.Services[s.Name] = s.State
			}
			st.ServicesFresh = true
		}
	}
	return st
}

// deliverNotifications drains the tenant's due outbox entries: claim in
// a transaction (SKIP LOCKED, retry pre-scheduled), send after commit,
// then record the outcome. Crashing between claim and send means a
// retry at the pre-scheduled time — at-least-once, never lost.
func (w *Worker) deliverNotifications(ctx context.Context, tenantID uuid.UUID) error {
	if w.Notify == nil {
		return nil
	}

	type job struct {
		delivery store.Delivery
		alert    store.Alert
		channel  store.NotificationChannel
	}
	var jobs []job
	err := w.Store.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		claimed, err := store.ClaimDueDeliveries(ctx, tx, deliveryBatch)
		if err != nil {
			return err
		}
		for _, d := range claimed {
			a, err := store.GetAlert(ctx, tx, d.AlertID)
			if err != nil {
				return fmt.Errorf("load alert %s: %w", d.AlertID, err)
			}
			ch, err := store.GetChannelWithSecret(ctx, tx, d.ChannelID)
			if err != nil {
				return fmt.Errorf("load channel %s: %w", d.ChannelID, err)
			}
			jobs = append(jobs, job{delivery: d, alert: a, channel: ch})
		}
		return nil
	})
	if err != nil {
		return err
	}

	for _, j := range jobs {
		sendErr := w.sendNotification(ctx, j.alert, j.channel, j.delivery.Event)
		err := w.Store.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
			if sendErr == nil {
				return store.MarkDeliverySent(ctx, tx, j.delivery.ID)
			}
			return store.MarkDeliveryError(ctx, tx, j.delivery.ID, j.delivery.Attempts, sendErr.Error())
		})
		if err != nil {
			w.Log.Error("record delivery outcome failed", "delivery_id", j.delivery.ID, "error", err)
		}
		if sendErr != nil {
			w.Log.Warn("notification delivery failed", "delivery_id", j.delivery.ID,
				"channel", j.channel.Name, "attempt", j.delivery.Attempts, "error", sendErr)
		}
	}
	return nil
}

func (w *Worker) sendNotification(ctx context.Context, a store.Alert, ch store.NotificationChannel, event string) error {
	switch ch.Type {
	case "email":
		var cfg struct {
			Recipients []string `json:"recipients"`
		}
		if err := json.Unmarshal(ch.Config, &cfg); err != nil || len(cfg.Recipients) == 0 {
			return fmt.Errorf("channel %s has no recipients", ch.ID)
		}
		subject, body := emailContent(a, event)
		return w.Notify.SendEmail(cfg.Recipients, subject, body)

	case "webhook":
		var cfg struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(ch.Config, &cfg); err != nil || cfg.URL == "" {
			return fmt.Errorf("channel %s has no url", ch.ID)
		}
		if w.Box == nil {
			return fmt.Errorf("webhook delivery needs the secrets box")
		}
		secret, err := w.Box.Open(ch.SecretEnc, ch.ID[:])
		if err != nil {
			return fmt.Errorf("unseal channel secret: %w", err)
		}
		payload, err := webhookPayload(a, event)
		if err != nil {
			return err
		}
		return w.Notify.SendWebhook(ctx, cfg.URL, secret, payload)
	}
	return fmt.Errorf("unknown channel type %q", ch.Type)
}

func emailContent(a store.Alert, event string) (subject, body string) {
	verb := "ALERT"
	if event == "resolved" {
		verb = "RESOLVED"
	}
	subject = fmt.Sprintf("[rmmagic] %s: %s", verb, a.Message)

	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", a.Message)
	fmt.Fprintf(&b, "Status:    %s\n", event)
	fmt.Fprintf(&b, "Severity:  %s\n", a.Severity)
	fmt.Fprintf(&b, "Device:    %s\n", a.Hostname)
	fmt.Fprintf(&b, "Rule:      %s\n", a.RuleType)
	fmt.Fprintf(&b, "Fired at:  %s\n", a.FiredAt.UTC().Format(time.RFC3339))
	if a.ResolvedAt != nil {
		fmt.Fprintf(&b, "Resolved:  %s\n", a.ResolvedAt.UTC().Format(time.RFC3339))
	}
	fmt.Fprintf(&b, "Alert ID:  %s\n", a.ID)
	return subject, b.String()
}

func webhookPayload(a store.Alert, event string) ([]byte, error) {
	return json.Marshal(map[string]any{
		"event": "alert." + event,
		"alert": map[string]any{
			"id":          a.ID,
			"device_id":   a.DeviceID,
			"hostname":    a.Hostname,
			"policy_id":   a.PolicyID,
			"rule_type":   a.RuleType,
			"severity":    a.Severity,
			"message":     a.Message,
			"details":     a.Details,
			"status":      a.Status,
			"fired_at":    a.FiredAt,
			"resolved_at": a.ResolvedAt,
		},
		"sent_at": time.Now().UTC(),
	})
}
