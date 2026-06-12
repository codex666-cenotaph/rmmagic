package collect

import (
	"bufio"
	"bytes"
	"context"
	"os/exec"
	"runtime"
	"strings"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
	gnet "github.com/shirou/gopsutil/v4/net"
)

type HardwareDisk struct {
	Device string `json:"device"`
	Mount  string `json:"mount"`
	FSType string `json:"fstype"`
	Total  int64  `json:"total"`
}

type HardwareNIC struct {
	Name string   `json:"name"`
	MAC  string   `json:"mac"`
	IPs  []string `json:"ips"`
}

type Hardware struct {
	Hostname        string         `json:"hostname"`
	Platform        string         `json:"platform"`
	PlatformVersion string         `json:"platform_version"`
	KernelVersion   string         `json:"kernel_version"`
	Virtualization  string         `json:"virtualization,omitempty"`
	CPUModel        string         `json:"cpu_model"`
	CPUCores        int            `json:"cpu_cores"`
	MemTotal        int64          `json:"mem_total"`
	Disks           []HardwareDisk `json:"disks"`
	NICs            []HardwareNIC  `json:"nics"`
}

type Package struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Arch    string `json:"arch,omitempty"`
}

type Service struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

// CollectHardware assembles the hardware snapshot.
func CollectHardware(ctx context.Context) (Hardware, error) {
	var hw Hardware

	if info, err := host.InfoWithContext(ctx); err == nil {
		hw.Hostname = info.Hostname
		hw.Platform = info.Platform
		hw.PlatformVersion = info.PlatformVersion
		hw.KernelVersion = info.KernelVersion
		hw.Virtualization = info.VirtualizationRole
	}

	if cpuInfos, err := cpu.InfoWithContext(ctx); err == nil && len(cpuInfos) > 0 {
		hw.CPUModel = cpuInfos[0].ModelName
		for _, c := range cpuInfos {
			hw.CPUCores += int(c.Cores)
		}
	}
	if hw.CPUCores == 0 {
		hw.CPUCores = runtime.NumCPU()
	}

	if vm, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		hw.MemTotal = int64(vm.Total)
	}

	if parts, err := disk.PartitionsWithContext(ctx, false); err == nil {
		seen := map[string]bool{}
		for _, p := range parts {
			if seen[p.Mountpoint] {
				continue
			}
			seen[p.Mountpoint] = true
			if u, err := disk.UsageWithContext(ctx, p.Mountpoint); err == nil && u.Total > 0 {
				hw.Disks = append(hw.Disks, HardwareDisk{
					Device: p.Device, Mount: p.Mountpoint,
					FSType: p.Fstype, Total: int64(u.Total),
				})
			}
		}
	}
	if hw.Disks == nil {
		hw.Disks = []HardwareDisk{}
	}

	if ifaces, err := gnet.InterfacesWithContext(ctx); err == nil {
		for _, iface := range ifaces {
			if iface.HardwareAddr == "" {
				continue
			}
			nic := HardwareNIC{Name: iface.Name, MAC: iface.HardwareAddr}
			for _, addr := range iface.Addrs {
				nic.IPs = append(nic.IPs, addr.Addr)
			}
			if nic.IPs == nil {
				nic.IPs = []string{}
			}
			hw.NICs = append(hw.NICs, nic)
		}
	}
	if hw.NICs == nil {
		hw.NICs = []HardwareNIC{}
	}

	return hw, nil
}

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
// when systemctl is not available (non-Linux, non-systemd).
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
