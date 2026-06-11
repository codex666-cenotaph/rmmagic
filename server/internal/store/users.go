package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type User struct {
	ID           uuid.UUID
	Email        string
	Status       string
	MFAEnabled   bool
	MFASecretEnc []byte
	PasswordHash string
	CreatedAt    time.Time
}

func GetUser(ctx context.Context, tx pgx.Tx, id uuid.UUID) (User, error) {
	var u User
	err := tx.QueryRow(ctx, `
		SELECT id, email, status, mfa_enabled, coalesce(mfa_secret_enc, ''), password_hash, created_at
		FROM users WHERE id = $1`, id).Scan(
		&u.ID, &u.Email, &u.Status, &u.MFAEnabled, &u.MFASecretEnc, &u.PasswordHash, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return u, ErrNotFound
	}
	return u, err
}

func ListUsers(ctx context.Context, tx pgx.Tx) ([]User, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, email, status, mfa_enabled, created_at
		FROM users ORDER BY email`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Email, &u.Status, &u.MFAEnabled, &u.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func CreateUser(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, email, passwordHash string) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO users (tenant_id, email, password_hash, status)
		VALUES ($1, $2, $3, 'active') RETURNING id`,
		tenantID, email, passwordHash).Scan(&id)
	return id, err
}

func SetUserStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status string) error {
	tag, err := tx.Exec(ctx,
		"UPDATE users SET status = $2, updated_at = now() WHERE id = $1", id, status)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// SetUserMFASecret stores the encrypted TOTP secret (enrollment pending
// until enabled).
func SetUserMFASecret(ctx context.Context, tx pgx.Tx, id uuid.UUID, secretEnc []byte) error {
	tag, err := tx.Exec(ctx,
		"UPDATE users SET mfa_secret_enc = $2, updated_at = now() WHERE id = $1", id, secretEnc)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func EnableUserMFA(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	tag, err := tx.Exec(ctx,
		"UPDATE users SET mfa_enabled = true, updated_at = now() WHERE id = $1", id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func AddRecoveryCodes(ctx context.Context, tx pgx.Tx, tenantID, userID uuid.UUID, codeHashes []string) error {
	for _, h := range codeHashes {
		if _, err := tx.Exec(ctx, `
			INSERT INTO mfa_recovery_codes (tenant_id, user_id, code_hash)
			VALUES ($1, $2, $3)`, tenantID, userID, h); err != nil {
			return err
		}
	}
	return nil
}

// ConsumeRecoveryCode marks the matching unused code as used; returns
// ErrNotFound when no unused code matches.
func ConsumeRecoveryCode(ctx context.Context, tx pgx.Tx, userID uuid.UUID, codeHash string) error {
	tag, err := tx.Exec(ctx, `
		UPDATE mfa_recovery_codes SET used_at = now()
		WHERE user_id = $1 AND code_hash = $2 AND used_at IS NULL`, userID, codeHash)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}
