// Package platform isolates OS-specific agent behaviour behind a small,
// uniform surface (PLAN: "App deployment: apt/dnf job type via
// internal/platform interface — interface cross-platform").
//
// Each OS provides its own implementation file selected by build tags:
//
//	platform_linux.go    dpkg/rpm packages, systemd services, /var/lib state
//	platform_windows.go  stubs today; registry/SCM collectors land next
//	platform_other.go    empty fallback so darwin dev builds keep working
//
// Collectors degrade gracefully: on hosts where a source is unavailable
// they return empty slices, never errors, so inventory reporting works the
// same way everywhere.
package platform

// Package is one installed software package.
type Package struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Arch    string `json:"arch,omitempty"`
}

// Service is one system service and its observed state.
type Service struct {
	Name  string `json:"name"`
	State string `json:"state"`
}
