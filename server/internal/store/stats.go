package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type StatsSample struct {
	TS       time.Time       `json:"ts"`
	CPUPct   float32         `json:"cpu_pct"`
	MemUsed  int64           `json:"mem_used"`
	MemTotal int64           `json:"mem_total"`
	Disks    json.RawMessage `json:"disks"`
	Net      json.RawMessage `json:"net"`
}

func InsertStats(ctx context.Context, tx pgx.Tx, tenantID, deviceID uuid.UUID, samples []StatsSample) error {
	// Batched INSERTs rather than COPY: Postgres rejects COPY FROM on
	// tables with row-level security, and RLS staying live on the ingest
	// path is worth more than COPY's throughput edge at this batch size.
	b := &pgx.Batch{}
	for _, s := range samples {
		disks := s.Disks
		if disks == nil {
			disks = json.RawMessage(`[]`)
		}
		net := s.Net
		if net == nil {
			net = json.RawMessage(`{}`)
		}
		b.Queue(`
			INSERT INTO device_stats (tenant_id, device_id, ts, cpu_pct, mem_used, mem_total, disks, net)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (device_id, ts) DO NOTHING`,
			tenantID, deviceID, s.TS, s.CPUPct, s.MemUsed, s.MemTotal, disks, net)
	}
	return tx.SendBatch(ctx, b).Close()
}

func ListStats(ctx context.Context, tx pgx.Tx, deviceID uuid.UUID, since, until time.Time, limit int) ([]StatsSample, error) {
	rows, err := tx.Query(ctx, `
		SELECT ts, cpu_pct, mem_used, mem_total, disks, net
		FROM device_stats
		WHERE device_id = $1 AND ts >= $2 AND ts <= $3
		ORDER BY ts ASC LIMIT $4`, deviceID, since, until, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StatsSample
	for rows.Next() {
		var s StatsSample
		if err := rows.Scan(&s.TS, &s.CPUPct, &s.MemUsed, &s.MemTotal, &s.Disks, &s.Net); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
