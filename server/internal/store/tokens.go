package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type APIToken struct {
	ID          uuid.UUID
	Name        string
	Permissions []string
	ScopeType   string
	ScopeID     *uuid.UUID
	LastUsedAt  *time.Time
	ExpiresAt   *time.Time
	RevokedAt   *time.Time
	CreatedAt   time.Time
}

func ListAPITokens(ctx context.Context, tx pgx.Tx) ([]APIToken, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, name, permissions, scope_type, scope_id,
		       last_used_at, expires_at, revoked_at, created_at
		FROM api_tokens ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIToken
	for rows.Next() {
		var t APIToken
		if err := rows.Scan(&t.ID, &t.Name, &t.Permissions, &t.ScopeType, &t.ScopeID,
			&t.LastUsedAt, &t.ExpiresAt, &t.RevokedAt, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func CreateAPIToken(ctx context.Context, tx pgx.Tx, tenantID, userID uuid.UUID, name string, tokenHash []byte, permissions []string, scopeType string, scopeID *uuid.UUID, expiresAt *time.Time) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO api_tokens (tenant_id, user_id, name, token_hash, permissions, scope_type, scope_id, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		tenantID, userID, name, tokenHash, permissions, scopeType, scopeID, expiresAt).Scan(&id)
	return id, err
}

func RevokeAPIToken(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	tag, err := tx.Exec(ctx, `
		UPDATE api_tokens SET revoked_at = now()
		WHERE id = $1 AND revoked_at IS NULL`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}
