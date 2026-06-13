//go:build !linux && !windows

package platform

import "context"

// Fallback for development hosts (e.g. darwin): the agent builds and runs,
// inventory sources are simply empty.

// DefaultStateDir is where the device identity and command journal live.
func DefaultStateDir() string { return "/var/lib/rmmagent" }

// HardenStateDir is a no-op on this OS.
func HardenStateDir(_ string) error { return nil }

// CollectPackages has no implementation on this OS; empty slice keeps
// reporting uniform.
func CollectPackages(ctx context.Context) ([]Package, error) {
	return []Package{}, nil
}

// CollectServices has no implementation on this OS; empty slice keeps
// reporting uniform.
func CollectServices(ctx context.Context) ([]Service, error) {
	return []Service{}, nil
}
