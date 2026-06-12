// Package store wraps the Postgres pool and enforces the tenant-scoping
// contract: all tenant data access goes through WithTenant, which sets
// app.tenant_id with transaction-local scope so RLS applies. Connections
// assume the rmm_app role, making RLS the backstop even for buggy SQL.
package store

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

// Open connects the pool. If appRole is non-empty every connection
// switches to it after connecting, so the RLS policies (which target
// rmm_app) are in force for all queries.
//
// Every connection gets statement/lock timeouts so a blocked query (e.g.
// an interrupted migration holding an ACCESS EXCLUSIVE lock) fails fast
// with a 500 instead of hanging requests and draining the pool.
func Open(ctx context.Context, dsn, appRole string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	if cfg.MaxConns < 10 {
		cfg.MaxConns = 10
	}
	if v := os.Getenv("RMM_DB_MAX_CONNS"); v != "" {
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("invalid RMM_DB_MAX_CONNS %q", v)
		}
		cfg.MaxConns = int32(n)
	}
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 30 * time.Minute

	setup := "SET lock_timeout = '3s'; " +
		"SET statement_timeout = '15s'; " +
		"SET idle_in_transaction_session_timeout = '15s'"
	if appRole != "" {
		setup += "; SET ROLE " + pgx.Identifier{appRole}.Sanitize()
	}
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, setup)
		return err
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

// WithTenant runs fn inside a transaction scoped to tenantID. The
// setting is transaction-local (set_config(..., true)), never
// session-level: connections are pooled and must not leak tenant state.
func (s *Store) WithTenant(ctx context.Context, tenantID uuid.UUID, fn func(pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			"SELECT set_config('app.tenant_id', $1, true)", tenantID.String()); err != nil {
			return fmt.Errorf("set tenant: %w", err)
		}
		return fn(tx)
	})
}

// System runs fn without a tenant scope. Only the SECURITY DEFINER
// auth_lookup_* functions return rows in this mode; everything else
// fails closed under RLS.
func (s *Store) System(ctx context.Context, fn func(pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, s.pool, fn)
}
