package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrNotFound = errors.New("not found")

// AuthUser is the unscoped login lookup result (SECURITY DEFINER path).
type AuthUser struct {
	UserID       uuid.UUID
	TenantID     uuid.UUID
	PasswordHash string
	MFAEnabled   bool
	Status       string
	TenantStatus string
}

func LookupUserByEmail(ctx context.Context, tx pgx.Tx, email string) (AuthUser, error) {
	var u AuthUser
	err := tx.QueryRow(ctx, "SELECT * FROM auth_lookup_user($1)", email).Scan(
		&u.UserID, &u.TenantID, &u.PasswordHash, &u.MFAEnabled, &u.Status, &u.TenantStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return u, ErrNotFound
	}
	return u, err
}

type AuthSession struct {
	TenantID  uuid.UUID
	UserID    uuid.UUID
	MFAPassed bool
	ExpiresAt time.Time
}

func LookupSession(ctx context.Context, tx pgx.Tx, tokenHash []byte) (AuthSession, error) {
	var s AuthSession
	err := tx.QueryRow(ctx, "SELECT * FROM auth_lookup_session($1)", tokenHash).Scan(
		&s.TenantID, &s.UserID, &s.MFAPassed, &s.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, ErrNotFound
	}
	return s, err
}

type AuthAPIToken struct {
	TokenID     uuid.UUID
	TenantID    uuid.UUID
	UserID      uuid.UUID
	Permissions []string
	ScopeType   string
	ScopeID     *uuid.UUID
	ExpiresAt   *time.Time
	RevokedAt   *time.Time
}

func LookupAPIToken(ctx context.Context, tx pgx.Tx, tokenHash []byte) (AuthAPIToken, error) {
	var t AuthAPIToken
	err := tx.QueryRow(ctx, "SELECT * FROM auth_lookup_api_token($1)", tokenHash).Scan(
		&t.TokenID, &t.TenantID, &t.UserID, &t.Permissions,
		&t.ScopeType, &t.ScopeID, &t.ExpiresAt, &t.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

func CreateSession(ctx context.Context, tx pgx.Tx, tokenHash []byte, tenantID, userID uuid.UUID, mfaPassed bool, ip string, ttl time.Duration) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO sessions (token_hash, tenant_id, user_id, mfa_passed, ip, expires_at)
		VALUES ($1, $2, $3, $4, nullif($5,'')::inet, now() + $6)`,
		tokenHash, tenantID, userID, mfaPassed, ip, ttl)
	return err
}

func UpgradeSessionMFA(ctx context.Context, tx pgx.Tx, tokenHash []byte) error {
	tag, err := tx.Exec(ctx,
		"UPDATE sessions SET mfa_passed = true WHERE token_hash = $1", tokenHash)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func DeleteSession(ctx context.Context, tx pgx.Tx, tokenHash []byte) error {
	_, err := tx.Exec(ctx, "DELETE FROM sessions WHERE token_hash = $1", tokenHash)
	return err
}

func TouchAPIToken(ctx context.Context, tx pgx.Tx, tokenID uuid.UUID) error {
	_, err := tx.Exec(ctx,
		"UPDATE api_tokens SET last_used_at = now() WHERE id = $1", tokenID)
	return err
}
