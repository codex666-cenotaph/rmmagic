//go:build linux

package platform

import (
	"bufio"
	"bytes"
	"context"
	"os/exec"
	"strings"
)

// DefaultStateDir is where the device identity and command journal live.
func DefaultStateDir() string { return "/var/lib/rmmagent" }

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
