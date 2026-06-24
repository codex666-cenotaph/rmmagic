//go:build !linux && !windows

package platform

import (
	"context"
	"errors"
)

// errPackagesUnsupported is returned by the package-management entry points
// on OSes without a supported package manager.
var errPackagesUnsupported = errors.New("package management is not supported on this OS")

// InstallPackages is unsupported on this OS.
func InstallPackages(_ context.Context, _ []string) ([]byte, error) {
	return nil, errPackagesUnsupported
}

// RemovePackages is unsupported on this OS.
func RemovePackages(_ context.Context, _ []string) ([]byte, error) {
	return nil, errPackagesUnsupported
}

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
