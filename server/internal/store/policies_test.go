package store

import (
	"testing"

	"github.com/google/uuid"
)

func strptr(s string) *string { return &s }

func TestPolicyAppliesToTag(t *testing.T) {
	dev := PolicyDeviceScope{
		DeviceID:   uuid.New(),
		SiteID:     uuid.New(),
		CustomerID: uuid.New(),
		Tags:       []string{"server", "eu-west"},
	}

	cases := []struct {
		name string
		pol  Policy
		want bool
	}{
		{"matching tag", Policy{ScopeType: "tag", ScopeTag: strptr("server")}, true},
		{"other matching tag", Policy{ScopeType: "tag", ScopeTag: strptr("eu-west")}, true},
		{"non-matching tag", Policy{ScopeType: "tag", ScopeTag: strptr("laptop")}, false},
		{"tag scope with nil tag", Policy{ScopeType: "tag"}, false},
		{"tenant always applies", Policy{ScopeType: "tenant"}, true},
		{"matching site", Policy{ScopeType: "site", ScopeID: &dev.SiteID}, true},
	}
	for _, c := range cases {
		if got := c.pol.AppliesTo(dev); got != c.want {
			t.Errorf("%s: AppliesTo = %v, want %v", c.name, got, c.want)
		}
	}

	// A device with no tags matches no tag-scoped policy.
	untagged := PolicyDeviceScope{DeviceID: uuid.New(), SiteID: uuid.New(), CustomerID: uuid.New()}
	if (Policy{ScopeType: "tag", ScopeTag: strptr("server")}).AppliesTo(untagged) {
		t.Errorf("untagged device must not match a tag policy")
	}
}
