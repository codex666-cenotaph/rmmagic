// Package alerts implements monitoring-policy resolution and alert
// evaluation. Everything here is pure (no I/O): the worker feeds it
// policies and device state from the store and acts on the returned
// findings, which keeps the merge and threshold semantics unit-testable.
package alerts

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
)

type Severity string

const (
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

// Rule type names; also the values of alerts.rule_type in the schema.
const (
	RuleCPUPct      = "cpu_pct"
	RuleMemPct      = "mem_pct"
	RuleDiskPct     = "disk_pct"
	RuleOffline     = "offline"
	RuleServiceDown = "service_down"
)

// ThresholdRule fires when a percentage metric is at or above Threshold.
type ThresholdRule struct {
	Threshold float64  `json:"threshold"`
	Severity  Severity `json:"severity,omitempty"`
}

// DiskRule fires per mount when usage is at or above Threshold. An
// empty Mounts list covers every mount the device reports.
type DiskRule struct {
	Threshold float64  `json:"threshold"`
	Mounts    []string `json:"mounts,omitempty"`
	Severity  Severity `json:"severity,omitempty"`
}

// OfflineRule fires when the device has not been seen for AfterS seconds.
type OfflineRule struct {
	AfterS   int      `json:"after_s"`
	Severity Severity `json:"severity,omitempty"`
}

// ServiceRule fires per service that is not in the running state.
type ServiceRule struct {
	Services []string `json:"services"`
	Severity Severity `json:"severity,omitempty"`
}

// Rules is the rule set of one policy; every field is optional.
type Rules struct {
	CPUPct      *ThresholdRule `json:"cpu_pct,omitempty"`
	MemPct      *ThresholdRule `json:"mem_pct,omitempty"`
	DiskPct     *DiskRule      `json:"disk_pct,omitempty"`
	Offline     *OfflineRule   `json:"offline,omitempty"`
	ServiceDown *ServiceRule   `json:"service_down,omitempty"`
}

func validSeverity(s Severity) bool {
	return s == "" || s == SeverityWarning || s == SeverityCritical
}

func (r Rules) Validate() error {
	if r.CPUPct == nil && r.MemPct == nil && r.DiskPct == nil && r.Offline == nil && r.ServiceDown == nil {
		return errors.New("rules must define at least one of cpu_pct, mem_pct, disk_pct, offline, service_down")
	}
	checkPct := func(name string, threshold float64, sev Severity) error {
		if threshold <= 0 || threshold > 100 {
			return fmt.Errorf("%s.threshold must be in (0, 100]", name)
		}
		if !validSeverity(sev) {
			return fmt.Errorf("%s.severity must be warning or critical", name)
		}
		return nil
	}
	if r.CPUPct != nil {
		if err := checkPct(RuleCPUPct, r.CPUPct.Threshold, r.CPUPct.Severity); err != nil {
			return err
		}
	}
	if r.MemPct != nil {
		if err := checkPct(RuleMemPct, r.MemPct.Threshold, r.MemPct.Severity); err != nil {
			return err
		}
	}
	if r.DiskPct != nil {
		if err := checkPct(RuleDiskPct, r.DiskPct.Threshold, r.DiskPct.Severity); err != nil {
			return err
		}
		for _, m := range r.DiskPct.Mounts {
			if m == "" || len(m) > 256 {
				return errors.New("disk_pct.mounts entries must be non-empty paths")
			}
		}
	}
	if r.Offline != nil {
		// Below 3 missed heartbeats everything flaps.
		if r.Offline.AfterS < 90 || r.Offline.AfterS > 86400*7 {
			return errors.New("offline.after_s must be between 90 and 604800")
		}
		if !validSeverity(r.Offline.Severity) {
			return errors.New("offline.severity must be warning or critical")
		}
	}
	if r.ServiceDown != nil {
		if len(r.ServiceDown.Services) == 0 || len(r.ServiceDown.Services) > 100 {
			return errors.New("service_down.services must list 1-100 services")
		}
		for _, s := range r.ServiceDown.Services {
			if s == "" || len(s) > 256 {
				return errors.New("service_down.services entries must be non-empty unit names")
			}
		}
		if !validSeverity(r.ServiceDown.Severity) {
			return errors.New("service_down.severity must be warning or critical")
		}
	}
	return nil
}

// ScopeRank orders policy scopes from broadest to most specific; used
// by Merge so the nearest scope wins per rule type. A tag scope sits
// above site (you opt a device in explicitly) but below a
// device-specific override.
func ScopeRank(scopeType string) int {
	switch scopeType {
	case "tenant":
		return 0
	case "customer":
		return 1
	case "site":
		return 2
	case "tag":
		return 3
	case "device":
		return 4
	default:
		return -1
	}
}

// maxScopeRank is the highest rank ScopeRank can return; Merge iterates
// ranks up to it.
const maxScopeRank = 4

// ScopedPolicy is one enabled policy already known to apply to the
// device under evaluation, tagged with its scope rank.
type ScopedPolicy struct {
	ID         uuid.UUID
	ScopeRank  int
	Rules      Rules
	ChannelIDs []uuid.UUID
}

// RuleWithSource is a merged rule plus the policy it came from (for
// alert attribution and notification routing).
type RuleWithSource[T any] struct {
	Rule       T
	PolicyID   uuid.UUID
	ChannelIDs []uuid.UUID
}

// Effective is the per-device result of policy inheritance resolution.
type Effective struct {
	CPUPct      *RuleWithSource[ThresholdRule]
	MemPct      *RuleWithSource[ThresholdRule]
	DiskPct     *RuleWithSource[DiskRule]
	Offline     *RuleWithSource[OfflineRule]
	ServiceDown *RuleWithSource[ServiceRule]
}

// Merge resolves policy inheritance for one device: policies are
// applied from broadest to most specific scope, so per rule type the
// nearest scope wins (device > site > customer > tenant). Ties at the
// same rank are broken by the caller's ordering (the store returns
// oldest first, so the newest policy at a rank wins).
func Merge(policies []ScopedPolicy) Effective {
	var eff Effective
	for rank := 0; rank <= maxScopeRank; rank++ {
		for _, p := range policies {
			if p.ScopeRank != rank {
				continue
			}
			if r := p.Rules.CPUPct; r != nil {
				eff.CPUPct = &RuleWithSource[ThresholdRule]{Rule: *r, PolicyID: p.ID, ChannelIDs: p.ChannelIDs}
			}
			if r := p.Rules.MemPct; r != nil {
				eff.MemPct = &RuleWithSource[ThresholdRule]{Rule: *r, PolicyID: p.ID, ChannelIDs: p.ChannelIDs}
			}
			if r := p.Rules.DiskPct; r != nil {
				eff.DiskPct = &RuleWithSource[DiskRule]{Rule: *r, PolicyID: p.ID, ChannelIDs: p.ChannelIDs}
			}
			if r := p.Rules.Offline; r != nil {
				eff.Offline = &RuleWithSource[OfflineRule]{Rule: *r, PolicyID: p.ID, ChannelIDs: p.ChannelIDs}
			}
			if r := p.Rules.ServiceDown; r != nil {
				eff.ServiceDown = &RuleWithSource[ServiceRule]{Rule: *r, PolicyID: p.ID, ChannelIDs: p.ChannelIDs}
			}
		}
	}
	return eff
}
