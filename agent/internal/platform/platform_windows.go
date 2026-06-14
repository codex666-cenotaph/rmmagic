//go:build windows

package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// InstallPackages is not yet implemented on Windows (a winget/choco backend
// lands in a later phase); package jobs report this as a failure.
func InstallPackages(_ context.Context, _ []string) ([]byte, error) {
	return nil, errors.New("package management is not yet supported on Windows")
}

// RemovePackages is not yet implemented on Windows.
func RemovePackages(_ context.Context, _ []string) ([]byte, error) {
	return nil, errors.New("package management is not yet supported on Windows")
}

// DefaultStateDir is where the device identity and command journal live.
func DefaultStateDir() string {
	base := os.Getenv("ProgramData")
	if base == "" {
		base = `C:\ProgramData`
	}
	return filepath.Join(base, "rmmagent")
}

// HardenStateDir sets a DACL on dir granting SYSTEM and built-in Administrators
// full control and removing inherited permissive ACEs.
func HardenStateDir(dir string) error {
	systemSID, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return fmt.Errorf("HardenStateDir system SID: %w", err)
	}
	adminsSID, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return fmt.Errorf("HardenStateDir admins SID: %w", err)
	}

	const (
		genericAll      = windows.ACCESS_MASK(0x10000000) // GENERIC_ALL
		inheritBoth     = uint32(0x3)                     // OBJECT_INHERIT_ACE | CONTAINER_INHERIT_ACE
	)

	ea := []windows.EXPLICIT_ACCESS{
		{
			AccessPermissions: genericAll,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       inheritBoth,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
				TrusteeValue: windows.TrusteeValueFromSID(systemSID),
			},
		},
		{
			AccessPermissions: genericAll,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       inheritBoth,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
				TrusteeValue: windows.TrusteeValueFromSID(adminsSID),
			},
		},
	}

	acl, err := windows.ACLFromEntries(ea, nil)
	if err != nil {
		return fmt.Errorf("HardenStateDir build ACL: %w", err)
	}

	return windows.SetNamedSecurityInfo(
		dir,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil,
	)
}

const (
	uninstallKey64 = `Software\Microsoft\Windows\CurrentVersion\Uninstall`
	uninstallKey32 = `Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`
)

// registryKey is the minimal interface over registry.Key used by package collectors.
// The narrow interface allows unit tests to inject in-memory fakes.
type registryKey interface {
	Close() error
	ReadSubKeyNames(n int) ([]string, error)
	OpenSubKey(path string) (registryKey, error)
	GetStringValue(name string) (string, error)
	GetIntegerValue(name string) (uint64, error)
}

// realKey wraps registry.Key to implement registryKey.
type realKey struct{ k registry.Key }

func (r realKey) Close() error                       { return r.k.Close() }
func (r realKey) ReadSubKeyNames(n int) ([]string, error) { return r.k.ReadSubKeyNames(n) }
func (r realKey) OpenSubKey(path string) (registryKey, error) {
	k, err := registry.OpenKey(r.k, path, registry.QUERY_VALUE)
	if err != nil {
		return nil, err
	}
	return realKey{k}, nil
}
func (r realKey) GetStringValue(name string) (string, error) {
	v, _, err := r.k.GetStringValue(name)
	return v, err
}
func (r realKey) GetIntegerValue(name string) (uint64, error) {
	v, _, err := r.k.GetIntegerValue(name)
	return v, err
}

// openUninstallRoot is the default opener for uninstall registry hives.
// Tests replace this variable to inject fake registry data.
var openUninstallRoot = func(hivePath string) (registryKey, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, hivePath, registry.READ)
	if err != nil {
		return nil, err
	}
	return realKey{k}, nil
}

// CollectPackages reads the Windows registry uninstall hives and returns
// installed software. Deduplicates entries present in both 32- and 64-bit views.
func CollectPackages(ctx context.Context) ([]Package, error) {
	seen := make(map[string]struct{})
	var pkgs []Package
	for _, hivePath := range [2]string{uninstallKey64, uninstallKey32} {
		for _, p := range readRegistryPackages(openUninstallRoot, hivePath) {
			key := p.Name + "\x00" + p.Version
			if _, dup := seen[key]; !dup {
				seen[key] = struct{}{}
				pkgs = append(pkgs, p)
			}
		}
	}
	if pkgs == nil {
		pkgs = []Package{}
	}
	return pkgs, nil
}

func readRegistryPackages(opener func(string) (registryKey, error), hivePath string) []Package {
	root, err := opener(hivePath)
	if err != nil {
		return nil
	}
	defer root.Close()

	names, err := root.ReadSubKeyNames(-1)
	if err != nil {
		return nil
	}

	var pkgs []Package
	for _, name := range names {
		sub, err := root.OpenSubKey(name)
		if err != nil {
			continue
		}
		p := readPackageEntry(sub)
		sub.Close()
		if p != nil {
			pkgs = append(pkgs, *p)
		}
	}
	return pkgs
}

func readPackageEntry(k registryKey) *Package {
	if v, err := k.GetIntegerValue("SystemComponent"); err == nil && v == 1 {
		return nil
	}
	name, err := k.GetStringValue("DisplayName")
	if err != nil || name == "" {
		return nil
	}
	version, _ := k.GetStringValue("DisplayVersion")
	return &Package{Name: name, Version: version}
}

// CollectServices enumerates the Service Control Manager and returns all
// services with their current state.
func CollectServices(ctx context.Context) ([]Service, error) {
	m, err := mgr.Connect()
	if err != nil {
		return []Service{}, nil
	}
	defer m.Disconnect()

	names, err := m.ListServices()
	if err != nil {
		return []Service{}, nil
	}

	svcs := make([]Service, 0, len(names))
	for _, name := range names {
		s, err := m.OpenService(name)
		if err != nil {
			continue
		}
		status, err := s.Query()
		s.Close()
		if err != nil {
			continue
		}
		svcs = append(svcs, Service{Name: name, State: mapServiceState(status.State)})
	}
	return svcs, nil
}

func mapServiceState(state svc.State) string {
	switch state {
	case svc.Running:
		return "running"
	case svc.Stopped:
		return "stopped"
	case svc.Paused:
		return "paused"
	case svc.StartPending:
		return "start_pending"
	case svc.StopPending:
		return "stop_pending"
	case svc.PausePending:
		return "pause_pending"
	case svc.ContinuePending:
		return "continue_pending"
	default:
		return "unknown"
	}
}
