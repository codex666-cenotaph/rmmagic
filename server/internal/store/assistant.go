package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// AssistantSettings is a tenant's in-dashboard AI assistant configuration.
// APIKeyEnc holds the sealed API key (secrets.Box ciphertext); it is never
// exposed beyond the server.
type AssistantSettings struct {
	Enabled   bool
	Provider  string
	Model     string
	APIKeyEnc []byte
}

// GetAssistantSettings returns the tenant's assistant settings. When no row
// exists it returns the zero value (disabled) and false.
func GetAssistantSettings(ctx context.Context, tx pgx.Tx) (AssistantSettings, bool, error) {
	var s AssistantSettings
	err := tx.QueryRow(ctx,
		`SELECT enabled, provider, model, api_key_enc FROM assistant_settings LIMIT 1`,
	).Scan(&s.Enabled, &s.Provider, &s.Model, &s.APIKeyEnc)
	if errors.Is(err, pgx.ErrNoRows) {
		return AssistantSettings{}, false, nil
	}
	if err != nil {
		return AssistantSettings{}, false, err
	}
	return s, true, nil
}

// UpsertAssistantSettings writes the tenant's assistant settings. When
// apiKeyEnc is nil the existing stored key is preserved (so the UI can
// update the model/provider without re-entering the secret).
func UpsertAssistantSettings(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID,
	enabled bool, provider, model string, apiKeyEnc []byte) error {
	if apiKeyEnc != nil {
		_, err := tx.Exec(ctx,
			`INSERT INTO assistant_settings (tenant_id, enabled, provider, model, api_key_enc, updated_at)
			 VALUES ($1, $2, $3, $4, $5, now())
			 ON CONFLICT (tenant_id) DO UPDATE SET
			   enabled = EXCLUDED.enabled, provider = EXCLUDED.provider,
			   model = EXCLUDED.model, api_key_enc = EXCLUDED.api_key_enc,
			   updated_at = now()`,
			tenantID, enabled, provider, model, apiKeyEnc)
		return err
	}
	_, err := tx.Exec(ctx,
		`INSERT INTO assistant_settings (tenant_id, enabled, provider, model, updated_at)
		 VALUES ($1, $2, $3, $4, now())
		 ON CONFLICT (tenant_id) DO UPDATE SET
		   enabled = EXCLUDED.enabled, provider = EXCLUDED.provider,
		   model = EXCLUDED.model, updated_at = now()`,
		tenantID, enabled, provider, model)
	return err
}
