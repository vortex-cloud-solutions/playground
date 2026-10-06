// Package db opens the API's connection pool.
package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Guard returns the statements every pooled connection runs before first
// use. They guard against the API's own bugs; they are not a privilege
// boundary, because the login role can RESET ROLE. The boundary is that the
// API accepts no SQL from clients.
func Guard(role string) []string {
	return []string{
		"SET ROLE " + pgx.Identifier{role}.Sanitize(),
		"SET default_transaction_read_only = on",
		"SET statement_timeout = '5s'",
	}
}

// Open returns a pool whose every connection runs Guard(role) in
// AfterConnect. It does not connect: a suspended serverless compute wakes on
// the first query, and /api/_health reports until then. The guard is session
// state, so dsn must reach Postgres in session mode, never through a
// transaction-mode pooler.
func Open(ctx context.Context, dsn, role string) (*pgxpool.Pool, error) {
	if dsn == "" {
		return nil, errors.New("PG_DSN is empty")
	}
	if role == "" {
		return nil, errors.New("PG_ROLE is empty")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		// pgx redacts passwords from this error only on a best-effort basis,
		// so it is never echoed.
		return nil, errors.New("PG_DSN is not a valid PostgreSQL connection string")
	}
	// Four connections serve a 2.5 CU compute; more would only queue there.
	cfg.MaxConns = 4
	stmts := Guard(role)
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		for _, s := range stmts {
			if _, err := conn.Exec(ctx, s); err != nil {
				// %v, not %w: a missing role is SQLSTATE 22023, and the API
				// maps class 22 to the visitor's bad_param. A failed guard is
				// a misconfiguration, so it must surface as unavailable.
				return fmt.Errorf("%s: %v", s, err)
			}
		}
		return nil
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}
	return pool, nil
}
