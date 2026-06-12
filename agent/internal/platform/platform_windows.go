//go:build windows

package platform

import (
	"context"
	"os"
	"path/filepath"
)

// DefaultStateDir is where the device identity and command journal live.
// ProgramData is machine-wide and ACL-restricted to administrators for
// directories we create (tightening to SYSTEM+Administrators explicitly
// happens at enroll time, phase 2).
func DefaultStateDir() string {
	base := os.Getenv("ProgramData")
	if base == "" {
		base = `C:\ProgramData`
	}
	return filepath.Join(base, "rmmagent")
}

// CollectPackages will read the registry uninstall hives
// (HKLM\...\CurrentVersion\Uninstall, 32+64 bit views). Stub until the
// Windows inventory phase; empty slice keeps reporting uniform.
func CollectPackages(ctx context.Context) ([]Package, error) {
	return []Package{}, nil
}

// CollectServices will enumerate the Service Control Manager. Stub until
// the Windows inventory phase; empty slice keeps reporting uniform.
func CollectServices(ctx context.Context) ([]Service, error) {
	return []Service{}, nil
}
