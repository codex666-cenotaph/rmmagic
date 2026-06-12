package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Script struct {
	ID          uuid.UUID
	Name        string
	Description string
	Language    string
	Body        string
	Parameters  json.RawMessage // [{name,description,default,required}]
	Version     int
	ArchivedAt  *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

const scriptSelect = `SELECT id, name, description, language, body, parameters,
    version, archived_at, created_at, updated_at FROM scripts`

func scanScript(row pgx.Row) (Script, error) {
	var s Script
	err := row.Scan(&s.ID, &s.Name, &s.Description, &s.Language, &s.Body,
		&s.Parameters, &s.Version, &s.ArchivedAt, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, ErrNotFound
	}
	return s, err
}

func ListScripts(ctx context.Context, tx pgx.Tx, includeArchived bool) ([]Script, error) {
	q := scriptSelect
	if !includeArchived {
		q += " WHERE archived_at IS NULL"
	}
	q += " ORDER BY name"
	rows, err := tx.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Script
	for rows.Next() {
		s, err := scanScript(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func GetScript(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Script, error) {
	return scanScript(tx.QueryRow(ctx, scriptSelect+" WHERE id = $1", id))
}

func CreateScript(ctx context.Context, tx pgx.Tx, tenantID, createdBy uuid.UUID,
	name, description, language, body string, parameters json.RawMessage) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO scripts (tenant_id, name, description, language, body, parameters, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
		tenantID, name, description, language, body, parameters, createdBy).Scan(&id)
	return id, err
}

func UpdateScript(ctx context.Context, tx pgx.Tx, id uuid.UUID,
	name, description, language, body string, parameters json.RawMessage) error {
	tag, err := tx.Exec(ctx, `
		UPDATE scripts SET name=$2, description=$3, language=$4, body=$5, parameters=$6,
		       version=version+1, updated_at=now()
		WHERE id=$1 AND archived_at IS NULL`,
		id, name, description, language, body, parameters)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func ArchiveScript(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	tag, err := tx.Exec(ctx,
		`UPDATE scripts SET archived_at=now() WHERE id=$1 AND archived_at IS NULL`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
