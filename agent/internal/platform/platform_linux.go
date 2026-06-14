//go:build linux

package platform

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
)

// DefaultStateDir is where the device identity and command journal live.
func DefaultStateDir() string { return "/var/lib/rmmagent" }

// HardenStateDir is a no-op on Linux; directory permissions are set by the
// package installer (nfpm) via the 0700 mode on /var/lib/rmmagent.
func HardenStateDir(_ string) error { return nil }

// CollectPackages collects installed packages via dpkg-query or rpm.
// Returns an empty slice (not an error) when no package manager is found.
func CollectPackages(ctx context.Context) ([]Package, error) {
	if pkgs, err := collectDpkg(ctx); err == nil {
		return pkgs, nil
	}
	if pkgs, err := collectRPM(ctx); err == nil {
		return pkgs, nil
	}
	return []Package{}, nil
}

func collectDpkg(ctx context.Context) ([]Package, error) {
	out, err := exec.CommandContext(ctx, "dpkg-query",
		"-W", "-f=${Package}\t${Version}\t${Architecture}\n").Output()
	if err != nil {
		return nil, err
	}
	return parseTSV(out), nil
}

func collectRPM(ctx context.Context) ([]Package, error) {
	out, err := exec.CommandContext(ctx, "rpm",
		"-qa", "--qf", "%{NAME}\t%{VERSION}-%{RELEASE}\t%{ARCH}\n").Output()
	if err != nil {
		return nil, err
	}
	return parseTSV(out), nil
}

func parseTSV(data []byte) []Package {
	var pkgs []Package
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := sc.Text()
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 2 || parts[0] == "" {
			continue
		}
		p := Package{Name: parts[0], Version: parts[1]}
		if len(parts) == 3 {
			p.Arch = parts[2]
		}
		pkgs = append(pkgs, p)
	}
	if pkgs == nil {
		pkgs = []Package{}
	}
	return pkgs
}

// InstallPackages installs the named packages with the host package
// manager (apt on Debian/Ubuntu, dnf/yum on RHEL family), returning the
// combined command output. Package names are validated by the caller; the
// manager is invoked non-interactively so jobs never block on a prompt.
func InstallPackages(ctx context.Context, names []string) ([]byte, error) {
	return runPackageManager(ctx, "install", names)
}

// RemovePackages uninstalls the named packages with the host package
// manager, returning the combined command output.
func RemovePackages(ctx context.Context, names []string) ([]byte, error) {
	return runPackageManager(ctx, "remove", names)
}

// errNoPackageManager is returned when neither apt nor dnf/yum is present.
var errNoPackageManager = errors.New("no supported package manager (apt/dnf/yum) found")

// runPackageManager builds and runs the install/remove command for the
// first available manager. apt-get is preferred when present, then dnf,
// then yum, matching collectDpkg/collectRPM precedence.
func runPackageManager(ctx context.Context, op string, names []string) ([]byte, error) {
	if len(names) == 0 {
		return nil, errors.New("no packages specified")
	}
	var argv []string
	switch {
	case have("apt-get"):
		// remove keeps config; the job model treats remove as uninstall.
		argv = append([]string{"apt-get", "-y", op}, names...)
	case have("dnf"):
		argv = append([]string{"dnf", "-y", op}, names...)
	case have("yum"):
		argv = append([]string{"yum", "-y", op}, names...)
	default:
		return nil, errNoPackageManager
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	// Non-interactive: apt must never prompt mid-job.
	cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	return cmd.CombinedOutput()
}

func have(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}

// CollectServices lists systemd service states. Returns an empty slice
// when systemctl is not available (non-systemd hosts).
func CollectServices(ctx context.Context) ([]Service, error) {
	out, err := exec.CommandContext(ctx, "systemctl",
		"list-units", "--type=service", "--no-pager", "--no-legend", "--all").Output()
	if err != nil {
		return []Service{}, nil
	}
	var svcs []Service
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		// Format: UNIT LOAD ACTIVE SUB DESCRIPTION
		fields := strings.Fields(sc.Text())
		if len(fields) < 4 {
			continue
		}
		name := strings.TrimSuffix(fields[0], ".service")
		if name == "" || strings.HasPrefix(name, "●") {
			continue
		}
		// sub-state (4th field): running/exited/failed/dead/...
		svcs = append(svcs, Service{Name: name, State: fields[3]})
	}
	if svcs == nil {
		svcs = []Service{}
	}
	return svcs, nil
}
