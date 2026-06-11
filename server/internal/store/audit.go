package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type AuditEntry struct {
	ID         uuid.UUID
	ActorType  string
	ActorID    *uuid.UUID
	Action     string
	TargetType *string
	TargetID   *uuid.UUID
	IP         *string
	Details    json.RawMessage
	CreatedAt  time.Time
}

// InsertAudit appends one audit entry. Callers must have redacted
// secrets from details before this point; nothing here inspects them.
func InsertAudit(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, e AuditEntry) error {
	details := e.Details
	if details == nil {
		details = json.RawMessage(`{}`)
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO audit_log (tenant_id, actor_type, actor_id, action, target_type, target_id, ip, details)
		VALUES ($1, $2, $3, $4, $5, $6, nullif($7,'')::inet, $8)`,
		tenantID, e.ActorType, e.ActorID, e.Action, e.TargetType, e.TargetID,
		deref(e.IP), details)
	return err
}

func ListAudit(ctx context.Context, tx pgx.Tx, before time.Time, limit int) ([]AuditEntry, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, actor_type, actor_id, action, target_type, target_id, ip::text, details, created_at
		FROM audit_log
		WHERE created_at < $1
		ORDER BY created_at DESC
		LIMIT $2`, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.ActorType, &e.ActorID, &e.Action,
			&e.TargetType, &e.TargetID, &e.IP, &e.Details, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
