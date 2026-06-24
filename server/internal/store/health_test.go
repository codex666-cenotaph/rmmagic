package store

import "testing"

func ptr(n int) *int { return &n }

func TestMapHealth(t *testing.T) {
	cases := []struct {
		name      string
		checkType string
		warnCodes []int32
		exitCode  *int
		output    string
		want      string
	}{
		{"exit zero healthy", CheckExitCode, nil, ptr(0), "", HealthHealthy},
		{"exit warning code", CheckExitCode, []int32{1}, ptr(1), "", HealthWarning},
		{"exit other critical", CheckExitCode, []int32{1}, ptr(2), "", HealthCritical},
		{"exit nil critical", CheckExitCode, nil, nil, "", HealthCritical},
		{"output healthy token", CheckOutput, nil, ptr(0), "all good\nHEALTH=healthy\n", HealthHealthy},
		{"output warning token", CheckOutput, nil, ptr(0), "HEALTH: warning", HealthWarning},
		{"output critical token", CheckOutput, nil, ptr(0), "HEALTH=critical", HealthCritical},
		{"output fail alias", CheckOutput, nil, ptr(0), "health=fail", HealthCritical},
		{"output last wins", CheckOutput, nil, ptr(0), "HEALTH=critical\nHEALTH=healthy", HealthHealthy},
		{"output no token unknown", CheckOutput, nil, ptr(0), "nothing here", HealthUnknown},
		{"none ignored", CheckNone, nil, ptr(0), "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _ := MapHealth(c.checkType, c.warnCodes, c.exitCode, c.output)
			if got != c.want {
				t.Fatalf("MapHealth(%q) = %q, want %q", c.checkType, got, c.want)
			}
		})
	}
}
