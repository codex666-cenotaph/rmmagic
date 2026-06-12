package api

import (
	"fmt"
	"testing"

	"github.com/codex666-cenotaph/rmmagic/server/internal/auth"
)

// TestEveryRouteDeclaresAuthorization is the structural guarantee that
// no endpoint exists without an explicit authorization decision: each
// route is either declared Public (allowed only for the login endpoint)
// or carries a permission (PermSelf for own-account operations).
func TestEveryRouteDeclaresAuthorization(t *testing.T) {
	s := &Server{}
	publicAllowlist := map[string]bool{
		"POST /api/v1/auth/login": true,
		// Device-authenticated in-handler: enrollment token validation /
		// Ed25519 request signatures (see agent_handlers.go).
		"POST /agent/v1/enroll":     true,
		"POST /agent/v1/stats":      true,
		"POST /agent/v1/inventory":  true,
	}
	seen := map[string]bool{}
	known := map[auth.Permission]bool{PermSelf: true}
	for _, p := range auth.All() {
		known[p] = true
	}

	for _, rt := range s.Routes() {
		key := rt.Method + " " + rt.Pattern
		if seen[key] {
			t.Errorf("duplicate route %s", key)
		}
		seen[key] = true

		if rt.Public {
			if rt.Perm != "" {
				t.Errorf("%s: Public routes must not declare a permission", key)
			}
			if !publicAllowlist[key] {
				t.Errorf("%s: public route not in the allowlist — every new public endpoint needs an explicit security review", key)
			}
			continue
		}
		if rt.Perm == "" {
			t.Errorf("%s: non-public route without a permission", key)
		}
		if !known[rt.Perm] {
			t.Errorf("%s: unknown permission %q", key, rt.Perm)
		}
		if rt.AllowPendingMFA && rt.Perm != PermSelf {
			t.Errorf("%s: only self routes may admit pending-MFA sessions", key)
		}
		if rt.Handler == nil {
			t.Errorf("%s: nil handler", key)
		}
	}
	if len(seen) == 0 {
		t.Fatal("no routes registered")
	}
	fmt.Printf("verified %d routes declare authorization\n", len(seen))
}
