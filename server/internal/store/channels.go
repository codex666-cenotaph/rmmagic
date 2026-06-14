package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type NotificationChannel struct {
	ID        uuid.UUID       `json:"id"`
	Name      string          `json:"name"`
	Type      string          `json:"type"` // email|webhook
	Config    json.RawMessage `json:"config"`
	SecretEnc []byte          `json:"-"` // sealed webhook secret; never sent to clients
	CreatedAt time.Time       `json:"created_at"`
}

func ListChannels(ctx context.Context, tx pgx.Tx) ([]NotificationChannel, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, name, type, config, created_at
		FROM notification_channels ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NotificationChannel
	for rows.Next() {
		var c NotificationChannel
		if err := rows.Scan(&c.ID, &c.Name, &c.Type, &c.Config, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetChannelWithSecret is the delivery-path lookup; the only caller
// that may see secret_enc is the worker's webhook sender.
func GetChannelWithSecret(ctx context.Context, tx pgx.Tx, id uuid.UUID) (NotificationChannel, error) {
	var c NotificationChannel
	err := tx.QueryRow(ctx, `
		SELECT id, name, type, config, secret_enc, created_at
		FROM notification_channels WHERE id = $1`, id).
		Scan(&c.ID, &c.Name, &c.Type, &c.Config, &c.SecretEnc, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

// CreateChannel inserts with a caller-generated ID so the webhook
// secret can be sealed with the channel ID as AEAD additional data
// before the row exists.
func CreateChannel(ctx context.Context, tx pgx.Tx, tenantID, id uuid.UUID,
	name, chType string, config json.RawMessage, secretEnc []byte, createdBy *uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO notification_channels (id, tenant_id, name, type, config, secret_enc, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		id, tenantID, name, chType, config, secretEnc, createdBy)
	return err
}

func UpdateChannel(ctx context.Context, tx pgx.Tx, id uuid.UUID,
	name, chType string, config json.RawMessage, secretEnc []byte) error {
	if len(secretEnc) > 0 {
		tag, err := tx.Exec(ctx, `
			UPDATE notification_channels
			SET name=$2, type=$3, config=$4, secret_enc=$5
			WHERE id=$1`,
			id, name, chType, config, secretEnc)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
	} else {
		tag, err := tx.Exec(ctx, `
			UPDATE notification_channels
			SET name=$2, type=$3, config=$4
			WHERE id=$1`,
			id, name, chType, config)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
	}
	return nil
}

func DeleteChannel(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	tag, err := tx.Exec(ctx, `DELETE FROM notification_channels WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
