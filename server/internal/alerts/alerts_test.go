package alerts

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMergeNearestScopeWinsPerRuleType(t *testing.T) {
	tenantPolicy := ScopedPolicy{
		ID: uuid.New(), ScopeRank: ScopeRank("tenant"),
		Rules: Rules{
			CPUPct:  &ThresholdRule{Threshold: 95},
			DiskPct: &DiskRule{Threshold: 90},
		},
		ChannelIDs: []uuid.UUID{uuid.New()},
	}
	customerPolicy := ScopedPolicy{
		ID: uuid.New(), ScopeRank: ScopeRank("customer"),
		Rules: Rules{DiskPct: &DiskRule{Threshold: 80, Severity: SeverityCritical}},
	}
	devicePolicy := ScopedPolicy{
		ID: uuid.New(), ScopeRank: ScopeRank("device"),
		Rules: Rules{Offline: &OfflineRule{AfterS: 300}},
	}

	// Order must not matter for cross-rank precedence.
	eff := Merge([]ScopedPolicy{devicePolicy, tenantPolicy, customerPolicy})

	if eff.CPUPct == nil || eff.CPUPct.PolicyID != tenantPolicy.ID {
		t.Fatalf("cpu_pct should come from the tenant policy")
	}
	if eff.DiskPct == nil || eff.DiskPct.PolicyID != customerPolicy.ID || eff.DiskPct.Rule.Threshold != 80 {
		t.Fatalf("disk_pct should be overridden by the customer policy, got %+v", eff.DiskPct)
	}
	if eff.Offline == nil || eff.Offline.PolicyID != devicePolicy.ID {
		t.Fatalf("offline should come from the device policy")
	}
	if eff.MemPct != nil || eff.ServiceDown != nil {
		t.Fatalf("unset rule types must stay nil")
	}
}

func TestMergeSameRankLastWins(t *testing.T) {
	older := ScopedPolicy{ID: uuid.New(), ScopeRank: 2, Rules: Rules{CPUPct: &ThresholdRule{Threshold: 95}}}
	newer := ScopedPolicy{ID: uuid.New(), ScopeRank: 2, Rules: Rules{CPUPct: &ThresholdRule{Threshold: 70}}}
	eff := Merge([]ScopedPolicy{older, newer})
	if eff.CPUPct.PolicyID != newer.ID {
		t.Fatalf("at equal rank the later policy must win")
	}
}

func TestRulesValidate(t *testing.T) {
	cases := []struct {
		name  string
		rules Rules
		ok    bool
	}{
		{"empty", Rules{}, false},
		{"valid cpu", Rules{CPUPct: &ThresholdRule{Threshold: 95}}, true},
		{"cpu over 100", Rules{CPUPct: &ThresholdRule{Threshold: 150}}, false},
		{"cpu zero", Rules{CPUPct: &ThresholdRule{Threshold: 0}}, false},
		{"bad severity", Rules{MemPct: &ThresholdRule{Threshold: 90, Severity: "panic"}}, false},
		{"offline too short", Rules{Offline: &OfflineRule{AfterS: 30}}, false},
		{"offline ok", Rules{Offline: &OfflineRule{AfterS: 300}}, true},
		{"service empty list", Rules{ServiceDown: &ServiceRule{Services: nil}}, false},
		{"service ok", Rules{ServiceDown: &ServiceRule{Services: []string{"nginx.service"}}}, true},
		{"disk ok", Rules{DiskPct: &DiskRule{Threshold: 90, Mounts: []string{"/"}}}, true},
		{"disk empty mount", Rules{DiskPct: &DiskRule{Threshold: 90, Mounts: []string{""}}}, false},
	}
	for _, c := range cases {
		if err := c.rules.Validate(); (err == nil) != c.ok {
			t.Errorf("%s: Validate() = %v, want ok=%v", c.name, err, c.ok)
		}
	}
}

func devState(deviceID uuid.UUID) DeviceState {
	lastSeen := time.Now().Add(-10 * time.Second)
	return DeviceState{
		DeviceID:       deviceID,
		Hostname:       "web-1",
		LastSeenAt:     &lastSeen,
		HasRecentStats: true,
		AvgCPUPct:      50,
		MemUsedPct:     50,
		Disks: []DiskUsage{
			{Mount: "/", Used: 93, Total: 100},
			{Mount: "/data", Used: 10, Total: 100},
		},
		ServicesFresh: true,
		Services:      map[string]string{"nginx.service": "running", "cron.service": "failed"},
	}
}

func effOf(p ScopedPolicy) Effective { return Merge([]ScopedPolicy{p}) }

func TestEvaluateDiskPerMount(t *testing.T) {
	id := uuid.New()
	eff := effOf(ScopedPolicy{ID: uuid.New(), ScopeRank: 0,
		Rules: Rules{DiskPct: &DiskRule{Threshold: 90}}})
	findings, evaluated := Evaluate(time.Now(), eff, devState(id))
	if len(findings) != 1 {
		t.Fatalf("want 1 finding (only / above 90%%), got %d: %+v", len(findings), findings)
	}
	f := findings[0]
	if f.RuleType != RuleDiskPct || f.DedupKey != id.String()+":disk_pct:/" {
		t.Fatalf("unexpected finding %+v", f)
	}
	if f.Severity != SeverityWarning {
		t.Fatalf("default severity must be warning, got %s", f.Severity)
	}
	if len(evaluated) != 1 || evaluated[0] != RuleDiskPct {
		t.Fatalf("evaluated = %v", evaluated)
	}
}

func TestEvaluateDiskMountFilter(t *testing.T) {
	id := uuid.New()
	eff := effOf(ScopedPolicy{ID: uuid.New(), ScopeRank: 0,
		Rules: Rules{DiskPct: &DiskRule{Threshold: 90, Mounts: []string{"/data"}}}})
	findings, _ := Evaluate(time.Now(), eff, devState(id))
	if len(findings) != 0 {
		t.Fatalf("/data is at 10%%; want no findings, got %+v", findings)
	}
}

func TestEvaluateThresholdsAndServices(t *testing.T) {
	id := uuid.New()
	st := devState(id)
	st.AvgCPUPct = 99
	st.MemUsedPct = 97
	eff := effOf(ScopedPolicy{ID: uuid.New(), ScopeRank: 0, Rules: Rules{
		CPUPct:      &ThresholdRule{Threshold: 95, Severity: SeverityCritical},
		MemPct:      &ThresholdRule{Threshold: 98},
		ServiceDown: &ServiceRule{Services: []string{"nginx.service", "cron.service", "missing.service"}},
	}})
	findings, evaluated := Evaluate(time.Now(), eff, st)

	byKey := map[string]Finding{}
	for _, f := range findings {
		byKey[f.DedupKey] = f
	}
	if f, ok := byKey[id.String()+":cpu_pct"]; !ok || f.Severity != SeverityCritical {
		t.Fatalf("cpu finding missing or wrong severity: %+v", findings)
	}
	if _, ok := byKey[id.String()+":mem_pct"]; ok {
		t.Fatalf("mem at 97%% under threshold 98%% must not fire")
	}
	if _, ok := byKey[id.String()+":service_down:cron.service"]; !ok {
		t.Fatalf("failed service must fire")
	}
	if _, ok := byKey[id.String()+":service_down:missing.service"]; !ok {
		t.Fatalf("unknown service must fire as not found")
	}
	if _, ok := byKey[id.String()+":service_down:nginx.service"]; ok {
		t.Fatalf("running service must not fire")
	}
	if len(evaluated) != 3 {
		t.Fatalf("evaluated = %v", evaluated)
	}
}

func TestEvaluateStaleDataNotEvaluated(t *testing.T) {
	id := uuid.New()
	st := devState(id)
	st.HasRecentStats = false
	st.ServicesFresh = false
	eff := effOf(ScopedPolicy{ID: uuid.New(), ScopeRank: 0, Rules: Rules{
		CPUPct:      &ThresholdRule{Threshold: 10},
		ServiceDown: &ServiceRule{Services: []string{"cron.service"}},
	}})
	findings, evaluated := Evaluate(time.Now(), eff, st)
	if len(findings) != 0 || len(evaluated) != 0 {
		t.Fatalf("stale data must be skipped entirely, got findings=%v evaluated=%v", findings, evaluated)
	}
}

func TestEvaluateOffline(t *testing.T) {
	id := uuid.New()
	st := devState(id)
	longAgo := time.Now().Add(-time.Hour)
	st.LastSeenAt = &longAgo
	eff := effOf(ScopedPolicy{ID: uuid.New(), ScopeRank: 0,
		Rules: Rules{Offline: &OfflineRule{AfterS: 300}}})

	findings, evaluated := Evaluate(time.Now(), eff, st)
	if len(findings) != 1 || findings[0].RuleType != RuleOffline {
		t.Fatalf("want offline finding, got %+v", findings)
	}
	if len(evaluated) != 1 || evaluated[0] != RuleOffline {
		t.Fatalf("evaluated = %v", evaluated)
	}

	// Recently seen: evaluated (so it can resolve) but not firing.
	st = devState(id)
	findings, evaluated = Evaluate(time.Now(), eff, st)
	if len(findings) != 0 || len(evaluated) != 1 {
		t.Fatalf("online device: findings=%v evaluated=%v", findings, evaluated)
	}

	// Never seen: provisioning, not an outage.
	st.LastSeenAt = nil
	findings, _ = Evaluate(time.Now(), eff, st)
	if len(findings) != 0 {
		t.Fatalf("never-seen device must not alert, got %+v", findings)
	}
}
