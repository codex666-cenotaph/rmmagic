package collect

import (
	"context"
	"runtime"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
	gnet "github.com/shirou/gopsutil/v4/net"

	"github.com/codex666-cenotaph/rmmagic/agent/internal/platform"
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

// Package and Service inventory comes from the OS-specific collectors in
// internal/platform; aliases keep the wire shapes defined in one place.
type (
	Package = platform.Package
	Service = platform.Service
)

// CollectHardware assembles the hardware snapshot (gopsutil: portable
// across linux/windows/darwin).
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

// CollectPackages reports installed software via the OS-specific collector
// (dpkg/rpm on Linux; registry on Windows when that phase lands).
func CollectPackages(ctx context.Context) ([]Package, error) {
	return platform.CollectPackages(ctx)
}

// CollectServices reports system services via the OS-specific collector
// (systemd on Linux; SCM on Windows when that phase lands).
func CollectServices(ctx context.Context) ([]Service, error) {
	return platform.CollectServices(ctx)
}
