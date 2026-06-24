package store

import (
	"testing"

	"github.com/google/uuid"
)

func TestJobTargetValidate(t *testing.T) {
	id := uuid.New()
	cases := []struct {
		name   string
		target JobTarget
		ok     bool
	}{
		{"devices", JobTarget{DeviceIDs: []uuid.UUID{id}}, true},
		{"site", JobTarget{SiteID: &id}, true},
		{"customer", JobTarget{CustomerID: &id}, true},
		{"os", JobTarget{OS: "linux"}, true},
		{"tag", JobTarget{Tag: "server"}, true},
		{"empty", JobTarget{}, false},
		{"two selectors", JobTarget{OS: "linux", Tag: "server"}, false},
		{"os and customer", JobTarget{OS: "linux", CustomerID: &id}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.target.Validate()
			if c.ok && err != nil {
				t.Fatalf("expected valid, got %v", err)
			}
			if !c.ok && err == nil {
				t.Fatalf("expected invalid, got nil")
			}
		})
	}
}
