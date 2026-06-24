package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// AppPackage is a centrally-managed, OS-specific app definition: what to
// install (Install, forwarded verbatim to the agent as a package_install
// job spec) and how to tell it is already present (Detection). JSON tags
// are snake_case to match the dashboard, mirroring Policy.
type AppPackage struct {
	ID          uuid.UUID       `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	OS          string          `json:"os"` // linux|windows|darwin
	Install     json.RawMessage `json:"install"`
	Detection   json.RawMessage `json:"detection"`
	TimeoutS    int             `json:"timeout_s"`
	Archived    bool            `json:"archived"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// AppDetection is the typed view of AppPackage.Detection. Method is the
// only supported method today ("package_name"); an empty Names list means
// "detect by the install package names" (i.e. by app name).
type AppDetection struct {
	Method string   `json:"method"`
	Names  []string `json:"names"`
}

// DeploymentRule binds one AppPackage to a scope with optional tag and
// hostname filters. The worker reconciles enabled rules hourly.
type DeploymentRule struct {
	ID          uuid.UUID       `json:"id"`
	PackageID   uuid.UUID       `json:"package_id"`
	PackageName string          `json:"package_name"`
	PackageOS   string          `json:"package_os"`
	Name        string          `json:"name"`
	ScopeType   string          `json:"scope_type"` // tenant|customer|site|device
	ScopeID     *uuid.UUID      `json:"scope_id"`
	Filters     json.RawMessage `json:"filters"`
	Enabled     bool            `json:"enabled"`
	LastRunAt   *time.Time      `json:"last_run_at"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// DeploymentFilters is the typed view of DeploymentRule.Filters. An empty
// filter set matches every device in scope (subject to the package OS).
type DeploymentFilters struct {
	Tags          []string `json:"tags,omitempty"`
	TagsMatch     string   `json:"tags_match,omitempty"` // any|all (default any)
	HostnameRegex string   `json:"hostname_regex,omitempty"`
}

// --- app_packages CRUD ----------------------------------------------------

const appPackageSelect = `
	SELECT id, name, description, os, install, detection, timeout_s,
	       archived_at IS NOT NULL, created_at, updated_at
	FROM app_packages`

func scanAppPackage(row pgx.Row) (AppPackage, error) {
	var p AppPackage
	err := row.Scan(&p.ID, &p.Name, &p.Description, &p.OS, &p.Install, &p.Detection,
		&p.TimeoutS, &p.Archived, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

// ListAppPackages returns packages, newest first. Archived packages are
// included only when includeArchived is set.
func ListAppPackages(ctx context.Context, tx pgx.Tx, includeArchived bool) ([]AppPackage, error) {
	q := appPackageSelect
	if !includeArchived {
		q += ` WHERE archived_at IS NULL`
	}
	q += ` ORDER BY created_at DESC`
	rows, err := tx.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AppPackage
	for rows.Next() {
		p, err := scanAppPackage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func GetAppPackage(ctx context.Context, tx pgx.Tx, id uuid.UUID) (AppPackage, error) {
	return scanAppPackage(tx.QueryRow(ctx, appPackageSelect+` WHERE id = $1`, id))
}

func CreateAppPackage(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, p AppPackage, createdBy *uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO app_packages (tenant_id, name, description, os, install, detection, timeout_s, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		tenantID, p.Name, p.Description, p.OS, p.Install, p.Detection, p.TimeoutS, createdBy).Scan(&id)
	return id, err
}

func UpdateAppPackage(ctx context.Context, tx pgx.Tx, p AppPackage) error {
	tag, err := tx.Exec(ctx, `
		UPDATE app_packages
		SET name=$2, description=$3, os=$4, install=$5, detection=$6, timeout_s=$7, updated_at=now()
		WHERE id=$1 AND archived_at IS NULL`,
		p.ID, p.Name, p.Description, p.OS, p.Install, p.Detection, p.TimeoutS)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ArchiveAppPackage soft-deletes a package. Its deployment rules cascade
// to nothing new (the worker skips archived packages), but existing rules
// remain visible until deleted.
func ArchiveAppPackage(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	tag, err := tx.Exec(ctx, `
		UPDATE app_packages SET archived_at = now(), updated_at = now()
		WHERE id = $1 AND archived_at IS NULL`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// --- app_deployment_rules CRUD --------------------------------------------

const deploymentRuleSelect = `
	SELECT r.id, r.package_id, p.name, p.os, r.name, r.scope_type, r.scope_id,
	       r.filters, r.enabled, r.last_run_at, r.created_at, r.updated_at
	FROM app_deployment_rules r
	JOIN app_packages p ON p.id = r.package_id`

func scanDeploymentRule(row pgx.Row) (DeploymentRule, error) {
	var r DeploymentRule
	err := row.Scan(&r.ID, &r.PackageID, &r.PackageName, &r.PackageOS, &r.Name,
		&r.ScopeType, &r.ScopeID, &r.Filters, &r.Enabled, &r.LastRunAt, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

func ListDeploymentRules(ctx context.Context, tx pgx.Tx) ([]DeploymentRule, error) {
	rows, err := tx.Query(ctx, deploymentRuleSelect+` ORDER BY r.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeploymentRule
	for rows.Next() {
		r, err := scanDeploymentRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func GetDeploymentRule(ctx context.Context, tx pgx.Tx, id uuid.UUID) (DeploymentRule, error) {
	return scanDeploymentRule(tx.QueryRow(ctx, deploymentRuleSelect+` WHERE r.id = $1`, id))
}

func CreateDeploymentRule(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, r DeploymentRule, createdBy *uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO app_deployment_rules (tenant_id, package_id, name, scope_type, scope_id, filters, enabled, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		tenantID, r.PackageID, r.Name, r.ScopeType, r.ScopeID, r.Filters, r.Enabled, createdBy).Scan(&id)
	return id, err
}

func UpdateDeploymentRule(ctx context.Context, tx pgx.Tx, r DeploymentRule) error {
	tag, err := tx.Exec(ctx, `
		UPDATE app_deployment_rules
		SET package_id=$2, name=$3, scope_type=$4, scope_id=$5, filters=$6, enabled=$7, updated_at=now()
		WHERE id=$1`,
		r.ID, r.PackageID, r.Name, r.ScopeType, r.ScopeID, r.Filters, r.Enabled)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func DeleteDeploymentRule(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	tag, err := tx.Exec(ctx, `DELETE FROM app_deployment_rules WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// --- reconciliation helpers -----------------------------------------------

// ClaimDueDeploymentRules atomically claims up to limit enabled rules whose
// package is live and whose last reconciliation is at least an hour old,
// advancing last_run_at in the same statement. FOR UPDATE SKIP LOCKED makes
// concurrent workers safe; each rule reconciles at most once per hour.
func ClaimDueDeploymentRules(ctx context.Context, tx pgx.Tx, limit int) ([]DeploymentRule, error) {
	rows, err := tx.Query(ctx, `
		WITH due AS (
			SELECT r.id
			FROM app_deployment_rules r
			JOIN app_packages p ON p.id = r.package_id
			WHERE r.enabled
			  AND p.archived_at IS NULL
			  AND (r.last_run_at IS NULL OR r.last_run_at < now() - interval '1 hour')
			ORDER BY r.last_run_at NULLS FIRST
			FOR UPDATE OF r SKIP LOCKED
			LIMIT $1
		)
		UPDATE app_deployment_rules r
		SET last_run_at = now()
		FROM due
		WHERE r.id = due.id
		RETURNING r.id, r.package_id,
		          (SELECT name FROM app_packages WHERE id = r.package_id),
		          (SELECT os FROM app_packages WHERE id = r.package_id),
		          r.name, r.scope_type, r.scope_id, r.filters, r.enabled,
		          r.last_run_at, r.created_at, r.updated_at`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeploymentRule
	for rows.Next() {
		r, err := scanDeploymentRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DevicesInScope returns the active devices a rule's scope resolves to,
// with the metadata the reconciler needs for filtering (os, hostname,
// tags). IDs outside the tenant resolve to nothing under RLS.
func DevicesInScope(ctx context.Context, tx pgx.Tx, scopeType string, scopeID *uuid.UUID) ([]Device, error) {
	q := deviceSelect
	var args []any
	switch scopeType {
	case "tenant":
		q += ` WHERE d.status = 'active'`
	case "customer":
		q += ` WHERE s.customer_id = $1 AND d.status = 'active'`
		args = append(args, scopeID)
	case "site":
		q += ` WHERE d.site_id = $1 AND d.status = 'active'`
		args = append(args, scopeID)
	case "device":
		q += ` WHERE d.id = $1 AND d.status = 'active'`
		args = append(args, scopeID)
	default:
		return nil, errors.New("invalid scope_type")
	}
	q += ` ORDER BY d.hostname`
	rows, err := tx.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Device
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeviceHasInstallInFlight reports whether the device already has an
// install of any of these packages in flight (queued/sent/running) or
// freshly succeeded within the last day. The success window bridges the
// gap between a successful install and the next inventory upload, so a
// rule does not re-push every hour while detection is still stale.
func DeviceHasInstallInFlight(ctx context.Context, tx pgx.Tx, deviceID uuid.UUID, packages []string) (bool, error) {
	if len(packages) == 0 {
		return false, nil
	}
	var exists bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM jobs
			WHERE device_id = $1
			  AND kind = 'package_install'
			  AND spec->'packages' ?| $2
			  AND (
			        status IN ('pending', 'sent', 'running')
			        OR (status = 'succeeded' AND finished_at > now() - interval '24 hours')
			      )
		)`, deviceID, packages).Scan(&exists)
	return exists, err
}
