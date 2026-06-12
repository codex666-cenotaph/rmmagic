// Package collect gathers endpoint statistics via gopsutil. The shapes
// mirror the server's ingest schema (docs/API.md).
package collect

import (
	"context"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	gnet "github.com/shirou/gopsutil/v4/net"
)

type DiskUsage struct {
	Mount string `json:"mount"`
	Used  uint64 `json:"used"`
	Total uint64 `json:"total"`
}

type NetCounters struct {
	RxBytes uint64 `json:"rx_bytes"`
	TxBytes uint64 `json:"tx_bytes"`
}

type Sample struct {
	TS       time.Time   `json:"ts"`
	CPUPct   float64     `json:"cpu_pct"`
	MemUsed  uint64      `json:"mem_used"`
	MemTotal uint64      `json:"mem_total"`
	Disks    []DiskUsage `json:"disks"`
	Net      NetCounters `json:"net"`
}

// Collect takes one sample. The CPU percentage is measured over a short
// window, so this call blocks ~1s.
func Collect(ctx context.Context) (Sample, error) {
	s := Sample{TS: time.Now().UTC(), Disks: []DiskUsage{}}

	if pcts, err := cpu.PercentWithContext(ctx, time.Second, false); err == nil && len(pcts) > 0 {
		s.CPUPct = pcts[0]
	}
	if vm, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		s.MemUsed, s.MemTotal = vm.Used, vm.Total
	}
	if parts, err := disk.PartitionsWithContext(ctx, false); err == nil {
		seen := map[string]bool{}
		for _, p := range parts {
			if seen[p.Mountpoint] {
				continue
			}
			seen[p.Mountpoint] = true
			if u, err := disk.UsageWithContext(ctx, p.Mountpoint); err == nil && u.Total > 0 {
				s.Disks = append(s.Disks, DiskUsage{Mount: p.Mountpoint, Used: u.Used, Total: u.Total})
			}
		}
	}
	if counters, err := gnet.IOCountersWithContext(ctx, false); err == nil && len(counters) > 0 {
		s.Net = NetCounters{RxBytes: counters[0].BytesRecv, TxBytes: counters[0].BytesSent}
	}
	return s, nil
}
