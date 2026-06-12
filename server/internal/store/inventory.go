package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Hardware mirrors the agent's HW inventory payload; stored as one
// jsonb document per device.
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

// SoftwarePackage is one installed package (dpkg/rpm).
type SoftwarePackage struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Arch    string `json:"arch,omitempty"`
}

// ServiceState is one systemd service and its current substate.
type ServiceState struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

func UpsertHardware(ctx context.Context, tx pgx.Tx, tenantID, deviceID uuid.UUID, hw Hardware, collectedAt time.Time) error {
	data, err := json.Marshal(hw)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO inventory_hw (device_id, tenant_id, data, collected_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (device_id) DO UPDATE
		SET data = EXCLUDED.data, collected_at = EXCLUDED.collected_at, updated_at = now()`,
		deviceID, tenantID, data, collectedAt)
	return err
}

func UpsertSoftware(ctx context.Context, tx pgx.Tx, tenantID, deviceID uuid.UUID, pkgs []SoftwarePackage, collectedAt time.Time) error {
	if pkgs == nil {
		pkgs = []SoftwarePackage{}
	}
	data, err := json.Marshal(pkgs)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO inventory_sw (device_id, tenant_id, packages, collected_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (device_id) DO UPDATE
		SET packages = EXCLUDED.packages, collected_at = EXCLUDED.collected_at, updated_at = now()`,
		deviceID, tenantID, data, collectedAt)
	return err
}

func UpsertServices(ctx context.Context, tx pgx.Tx, tenantID, deviceID uuid.UUID, services []ServiceState) error {
	if services == nil {
		services = []ServiceState{}
	}
	data, err := json.Marshal(services)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO device_services (device_id, tenant_id, services, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (device_id) DO UPDATE
		SET services = EXCLUDED.services, updated_at = now()`,
		deviceID, tenantID, data)
	return err
}

// Inventory is the dashboard view: HW document, package list, and the
// latest service states, each with its own freshness timestamp.
type Inventory struct {
	HW                json.RawMessage
	HWCollectedAt     *time.Time
	Packages          json.RawMessage
	SWCollectedAt     *time.Time
	Services          json.RawMessage
	ServicesUpdatedAt *time.Time
}

func GetInventory(ctx context.Context, tx pgx.Tx, deviceID uuid.UUID) (Inventory, error) {
	var inv Inventory
	err := tx.QueryRow(ctx,
		`SELECT data, collected_at FROM inventory_hw WHERE device_id = $1`, deviceID).
		Scan(&inv.HW, &inv.HWCollectedAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return inv, err
	}
	err = tx.QueryRow(ctx,
		`SELECT packages, collected_at FROM inventory_sw WHERE device_id = $1`, deviceID).
		Scan(&inv.Packages, &inv.SWCollectedAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return inv, err
	}
	err = tx.QueryRow(ctx,
		`SELECT services, updated_at FROM device_services WHERE device_id = $1`, deviceID).
		Scan(&inv.Services, &inv.ServicesUpdatedAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return inv, err
	}
	return inv, nil
}
