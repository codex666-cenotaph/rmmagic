package alerts

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// DiskUsage is the latest reported usage of one mount.
type DiskUsage struct {
	Mount string
	Used  int64
	Total int64
}

// DeviceState is everything Evaluate needs about one device. Stats
// fields are only meaningful when HasRecentStats is true; Services only
// when ServicesFresh is true. Stale data is never evaluated — neither
// to fire nor to resolve — so a device going quiet doesn't silently
// clear its CPU alert.
type DeviceState struct {
	DeviceID   uuid.UUID
	Hostname   string
	LastSeenAt *time.Time

	HasRecentStats bool
	AvgCPUPct      float64 // average over the evaluation window
	MemUsedPct     float64 // latest sample
	Disks          []DiskUsage

	ServicesFresh bool
	Services      map[string]string // unit name -> substate (running, exited, failed, ...)
}

// Finding is one condition that currently holds for a device.
type Finding struct {
	PolicyID   uuid.UUID
	ChannelIDs []uuid.UUID
	RuleType   string
	DedupKey   string
	Severity   Severity
	Message    string
	Details    map[string]any
}

func severityOr(s Severity) Severity {
	if s == "" {
		return SeverityWarning
	}
	return s
}

// DedupKey identifies one alert condition; extra distinguishes
// conditions within a rule type (mount, service name).
func DedupKey(deviceID uuid.UUID, ruleType, extra string) string {
	k := deviceID.String() + ":" + ruleType
	if extra != "" {
		k += ":" + extra
	}
	return k
}

// Evaluate checks the effective rules against the device state. It
// returns the conditions that hold now, plus the rule types that were
// actually assessed: the worker only auto-resolves open alerts whose
// rule type was evaluated this pass, so stale telemetry never resolves
// an alert it cannot confirm.
func Evaluate(now time.Time, eff Effective, st DeviceState) (findings []Finding, evaluated []string) {
	if eff.Offline != nil {
		evaluated = append(evaluated, RuleOffline)
		offlineFor := time.Duration(0)
		if st.LastSeenAt != nil {
			offlineFor = now.Sub(*st.LastSeenAt)
		}
		// Never-seen devices don't alert: enrollment without a first
		// connection is a provisioning state, not an outage.
		if st.LastSeenAt != nil && offlineFor >= time.Duration(eff.Offline.Rule.AfterS)*time.Second {
			findings = append(findings, Finding{
				PolicyID:   eff.Offline.PolicyID,
				ChannelIDs: eff.Offline.ChannelIDs,
				RuleType:   RuleOffline,
				DedupKey:   DedupKey(st.DeviceID, RuleOffline, ""),
				Severity:   severityOr(eff.Offline.Rule.Severity),
				Message: fmt.Sprintf("Device %s is offline (last seen %s ago)",
					st.Hostname, offlineFor.Round(time.Minute)),
				Details: map[string]any{
					"after_s":      eff.Offline.Rule.AfterS,
					"last_seen_at": st.LastSeenAt,
				},
			})
		}
	}

	if st.HasRecentStats {
		if eff.CPUPct != nil {
			evaluated = append(evaluated, RuleCPUPct)
			if st.AvgCPUPct >= eff.CPUPct.Rule.Threshold {
				findings = append(findings, Finding{
					PolicyID:   eff.CPUPct.PolicyID,
					ChannelIDs: eff.CPUPct.ChannelIDs,
					RuleType:   RuleCPUPct,
					DedupKey:   DedupKey(st.DeviceID, RuleCPUPct, ""),
					Severity:   severityOr(eff.CPUPct.Rule.Severity),
					Message: fmt.Sprintf("CPU usage on %s is %.0f%% (threshold %.0f%%)",
						st.Hostname, st.AvgCPUPct, eff.CPUPct.Rule.Threshold),
					Details: map[string]any{
						"cpu_pct":   st.AvgCPUPct,
						"threshold": eff.CPUPct.Rule.Threshold,
					},
				})
			}
		}
		if eff.MemPct != nil {
			evaluated = append(evaluated, RuleMemPct)
			if st.MemUsedPct >= eff.MemPct.Rule.Threshold {
				findings = append(findings, Finding{
					PolicyID:   eff.MemPct.PolicyID,
					ChannelIDs: eff.MemPct.ChannelIDs,
					RuleType:   RuleMemPct,
					DedupKey:   DedupKey(st.DeviceID, RuleMemPct, ""),
					Severity:   severityOr(eff.MemPct.Rule.Severity),
					Message: fmt.Sprintf("Memory usage on %s is %.0f%% (threshold %.0f%%)",
						st.Hostname, st.MemUsedPct, eff.MemPct.Rule.Threshold),
					Details: map[string]any{
						"mem_pct":   st.MemUsedPct,
						"threshold": eff.MemPct.Rule.Threshold,
					},
				})
			}
		}
		if eff.DiskPct != nil {
			evaluated = append(evaluated, RuleDiskPct)
			watched := map[string]bool{}
			for _, m := range eff.DiskPct.Rule.Mounts {
				watched[m] = true
			}
			for _, d := range st.Disks {
				if len(watched) > 0 && !watched[d.Mount] {
					continue
				}
				if d.Total <= 0 {
					continue
				}
				pct := float64(d.Used) / float64(d.Total) * 100
				if pct >= eff.DiskPct.Rule.Threshold {
					findings = append(findings, Finding{
						PolicyID:   eff.DiskPct.PolicyID,
						ChannelIDs: eff.DiskPct.ChannelIDs,
						RuleType:   RuleDiskPct,
						DedupKey:   DedupKey(st.DeviceID, RuleDiskPct, d.Mount),
						Severity:   severityOr(eff.DiskPct.Rule.Severity),
						Message: fmt.Sprintf("Disk usage on %s %s is %.0f%% (threshold %.0f%%)",
							st.Hostname, d.Mount, pct, eff.DiskPct.Rule.Threshold),
						Details: map[string]any{
							"mount":     d.Mount,
							"disk_pct":  pct,
							"used":      d.Used,
							"total":     d.Total,
							"threshold": eff.DiskPct.Rule.Threshold,
						},
					})
				}
			}
		}
	}

	if eff.ServiceDown != nil && st.ServicesFresh {
		evaluated = append(evaluated, RuleServiceDown)
		for _, name := range eff.ServiceDown.Rule.Services {
			state, known := st.Services[name]
			if known && state == "running" {
				continue
			}
			if !known {
				state = "not found"
			}
			findings = append(findings, Finding{
				PolicyID:   eff.ServiceDown.PolicyID,
				ChannelIDs: eff.ServiceDown.ChannelIDs,
				RuleType:   RuleServiceDown,
				DedupKey:   DedupKey(st.DeviceID, RuleServiceDown, name),
				Severity:   severityOr(eff.ServiceDown.Rule.Severity),
				Message: fmt.Sprintf("Service %s on %s is not running (%s)",
					name, st.Hostname, state),
				Details: map[string]any{
					"service": name,
					"state":   state,
				},
			})
		}
	}

	return findings, evaluated
}
