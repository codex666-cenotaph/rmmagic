package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type EnrollmentToken struct {
	ID        uuid.UUID
	SiteID    uuid.UUID
	SiteName  string
	ExpiresAt time.Time
	MaxUses   int
	UseCount  int
	CreatedBy *uuid.UUID
	RevokedAt *time.Time
	CreatedAt time.Time
}

func ListEnrollmentTokens(ctx context.Context, tx pgx.Tx) ([]EnrollmentToken, error) {
	rows, err := tx.Query(ctx, `
		SELECT t.id, t.site_id, s.name, t.expires_at, t.max_uses, t.use_count,
		       t.created_by, t.revoked_at, t.created_at
		FROM enrollment_tokens t JOIN sites s ON s.id = t.site_id
		ORDER BY t.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EnrollmentToken
	for rows.Next() {
		var t EnrollmentToken
		if err := rows.Scan(&t.ID, &t.SiteID, &t.SiteName, &t.ExpiresAt, &t.MaxUses,
			&t.UseCount, &t.CreatedBy, &t.RevokedAt, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func CreateEnrollmentToken(ctx context.Context, tx pgx.Tx, tenantID, siteID, createdBy uuid.UUID, tokenHash []byte, expiresAt time.Time, maxUses int) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO enrollment_tokens (tenant_id, site_id, token_hash, expires_at, max_uses, created_by)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		tenantID, siteID, tokenHash, expiresAt, maxUses, createdBy).Scan(&id)
	return id, err
}

func RevokeEnrollmentToken(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	tag, err := tx.Exec(ctx, `
		UPDATE enrollment_tokens SET revoked_at = now()
		WHERE id = $1 AND revoked_at IS NULL`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// ConsumeEnrollmentToken atomically increments use_count, failing when
// the token is exhausted (guards concurrent enrollment races).
func ConsumeEnrollmentToken(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	tag, err := tx.Exec(ctx, `
		UPDATE enrollment_tokens SET use_count = use_count + 1
		WHERE id = $1 AND use_count < max_uses AND revoked_at IS NULL AND expires_at > now()`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// AuthEnrollmentToken is the unscoped enrollment lookup (SECURITY DEFINER).
type AuthEnrollmentToken struct {
	TokenID   uuid.UUID
	TenantID  uuid.UUID
	SiteID    uuid.UUID
	ExpiresAt time.Time
	MaxUses   int
	UseCount  int
	RevokedAt *time.Time
}

func LookupEnrollmentToken(ctx context.Context, tx pgx.Tx, tokenHash []byte) (AuthEnrollmentToken, error) {
	var t AuthEnrollmentToken
	err := tx.QueryRow(ctx, "SELECT * FROM auth_lookup_enrollment_token($1)", tokenHash).Scan(
		&t.TokenID, &t.TenantID, &t.SiteID, &t.ExpiresAt, &t.MaxUses, &t.UseCount, &t.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}
