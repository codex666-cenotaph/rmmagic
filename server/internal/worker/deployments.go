package worker

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/codex666-cenotaph/rmmagic/server/internal/store"
)

const (
	// dueRuleClaims caps deployment rules reconciled per tenant per tick.
	dueRuleClaims = 100
	// deployJobExpiry is the queue window for a reconciliation-created
	// install job: an offline device has this long to come back and run it.
	deployJobExpiry = 24 * time.Hour
)

// reconcileDeployments claims due deployment rules (at most hourly each)
// and, for every device in scope that passes the rule's filters and does
// not already have the app, creates a package_install job. Jobs are
// created in the claiming transaction; delivery to online agents happens
// after commit, mirroring schedule firing.
func (w *Worker) reconcileDeployments(ctx context.Context, tenantID uuid.UUID) error {
	var created []store.PendingJob
	err := w.Store.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		rules, err := store.ClaimDueDeploymentRules(ctx, tx, dueRuleClaims)
		if err != nil {
			return err
		}
		for _, rule := range rules {
			jobs, err := w.reconcileRule(ctx, tx, tenantID, rule)
			if err != nil {
				return err
			}
			created = append(created, jobs...)
		}
		return nil
	})
	if err != nil {
		return err
	}

	if w.Gateway != nil {
		for _, j := range created {
			if sent := w.Gateway.DispatchJob(ctx, tenantID, j.DeviceID, j.JobID, j.CommandID); sent {
				_ = w.Store.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
					return store.MarkJobSent(ctx, tx, j.JobID)
				})
			}
		}
	}
	return nil
}

func (w *Worker) reconcileRule(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, rule store.DeploymentRule) ([]store.PendingJob, error) {
	pkg, err := store.GetAppPackage(ctx, tx, rule.PackageID)
	if err != nil || pkg.Archived {
		// Package vanished or was archived between claim and read; skip.
		return nil, nil
	}

	var installSpec store.PackageSpecJSON
	if err := json.Unmarshal(pkg.Install, &installSpec); err != nil || len(installSpec.Packages) == 0 {
		w.Log.Warn("deployment rule: package has no install spec", "rule_id", rule.ID, "package_id", pkg.ID)
		return nil, nil
	}
	var detection store.AppDetection
	_ = json.Unmarshal(pkg.Detection, &detection)
	// Empty detection list means "detect by the install package names".
	detectNames := detection.Names
	if len(detectNames) == 0 {
		detectNames = installSpec.Packages
	}

	var filters store.DeploymentFilters
	_ = json.Unmarshal(rule.Filters, &filters)
	var hostRe *regexp.Regexp
	if filters.HostnameRegex != "" {
		hostRe, err = regexp.Compile(filters.HostnameRegex)
		if err != nil {
			w.Log.Warn("deployment rule: invalid hostname_regex", "rule_id", rule.ID, "error", err)
			return nil, nil
		}
	}

	devices, err := store.DevicesInScope(ctx, tx, rule.ScopeType, rule.ScopeID)
	if err != nil {
		return nil, err
	}

	expiresAt := time.Now().Add(deployJobExpiry)
	var created []store.PendingJob
	for _, dev := range devices {
		if !deviceMatchesRule(dev, pkg.OS, filters, hostRe) {
			continue
		}
		// Already installed? Inventory is the source of truth.
		installed, err := deviceHasApp(ctx, tx, dev.ID, detectNames)
		if err != nil {
			return nil, err
		}
		if installed {
			continue
		}
		// Don't pile on if an install is already queued/running or just
		// succeeded (inventory may not have refreshed yet).
		inFlight, err := store.DeviceHasInstallInFlight(ctx, tx, dev.ID, installSpec.Packages)
		if err != nil {
			return nil, err
		}
		if inFlight {
			continue
		}
		jobID, commandID, err := store.CreatePackageJob(ctx, tx,
			tenantID, dev.ID, nil, nil, "package_install", pkg.TimeoutS, expiresAt, pkg.Install)
		if err != nil {
			return nil, err
		}
		created = append(created, store.PendingJob{JobID: jobID, CommandID: commandID, DeviceID: dev.ID})
	}

	if len(created) > 0 {
		ruleID := rule.ID
		if err := store.InsertAudit(ctx, tx, tenantID, store.AuditEntry{
			ActorType: "system", Action: "deployment_rule.reconcile",
			TargetType: strPtr("deployment_rule"), TargetID: &ruleID,
			Details: mustJSONRaw(map[string]any{
				"rule_name": rule.Name, "package_name": pkg.Name, "jobs_created": len(created),
			}),
		}); err != nil {
			return nil, err
		}
		w.Log.Info("deployment rule reconciled", "rule_id", rule.ID, "name", rule.Name, "jobs", len(created))
	}
	return created, nil
}

// deviceMatchesRule reports whether a device passes a rule's OS/tag/
// hostname filters. The package OS is mandatory; a rule never offers an
// app to a device of a different platform.
func deviceMatchesRule(dev store.Device, packageOS string, f store.DeploymentFilters, hostRe *regexp.Regexp) bool {
	if !strings.EqualFold(dev.OS, packageOS) {
		return false
	}
	if len(f.Tags) > 0 && !tagsMatch(dev.Tags, f.Tags, f.TagsMatch) {
		return false
	}
	if hostRe != nil && !hostRe.MatchString(dev.Hostname) {
		return false
	}
	return true
}

// tagsMatch implements the any/all semantics over a device's tag set.
func tagsMatch(deviceTags, want []string, mode string) bool {
	has := make(map[string]bool, len(deviceTags))
	for _, t := range deviceTags {
		has[t] = true
	}
	if mode == "all" {
		for _, t := range want {
			if !has[t] {
				return false
			}
		}
		return true
	}
	// default: any
	for _, t := range want {
		if has[t] {
			return true
		}
	}
	return false
}

// deviceHasApp reports whether the device's software inventory contains
// every detection name (case-insensitive). A device with no inventory is
// treated as not having the app, so the rule will offer the install.
func deviceHasApp(ctx context.Context, tx pgx.Tx, deviceID uuid.UUID, names []string) (bool, error) {
	if len(names) == 0 {
		return false, nil
	}
	inv, err := store.GetInventory(ctx, tx, deviceID)
	if err != nil {
		return false, err
	}
	if len(inv.Packages) == 0 {
		return false, nil
	}
	var pkgs []store.SoftwarePackage
	if err := json.Unmarshal(inv.Packages, &pkgs); err != nil {
		return false, nil
	}
	present := make(map[string]bool, len(pkgs))
	for _, p := range pkgs {
		present[strings.ToLower(p.Name)] = true
	}
	for _, n := range names {
		if !present[strings.ToLower(n)] {
			return false, nil
		}
	}
	return true, nil
}
