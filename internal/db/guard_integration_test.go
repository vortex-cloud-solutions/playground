package db_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vortex-cloud-solutions/playground/internal/db"
	"github.com/vortex-cloud-solutions/playground/internal/pgtest"
)

const (
	write = `INSERT INTO hello.greetings (id, lang, country, word, seen_on) VALUES (1000, 'xx', 'XX', 'x', '2026-10-06')`
	sleep = `SELECT pg_sleep(6)`
)

func sqlstate(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

func TestGuardOnARealServer(t *testing.T) {
	pg := pgtest.Start(t)
	pgtest.ApplySchema(t, pg, "hello")
	ctx := context.Background()

	// Negative control: the same login role without the guard writes and
	// sleeps freely, so the failures below are the guard's doing.
	plain, err := pgxpool.New(ctx, pg.APIDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	if _, err := plain.Exec(ctx, write); err != nil {
		t.Fatalf("control: write without the guard failed: %v", err)
	}
	if _, err := plain.Exec(ctx, sleep); err != nil {
		t.Fatalf("control: pg_sleep(6) without the guard failed: %v", err)
	}

	pool, err := db.Open(ctx, pg.APIDSN, "playground_ro")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	var user, readOnly, timeout string
	var greetings int
	err = pool.QueryRow(ctx, `SELECT current_user, current_setting('default_transaction_read_only'),
		current_setting('statement_timeout'), (SELECT count(*) FROM hello.greetings)`).Scan(&user, &readOnly, &timeout, &greetings)
	if err != nil {
		t.Fatal(err)
	}
	// 7 rows: the six from schema.sql plus the control's write.
	if user != "playground_ro" || readOnly != "on" || timeout != "5s" || greetings != 7 {
		t.Errorf("session = (%s, %s, %s, %d rows), want (playground_ro, on, 5s, 7 rows)", user, readOnly, timeout, greetings)
	}

	_, err = pool.Exec(ctx, write)
	if got := sqlstate(err); got != "25006" {
		t.Errorf("write under the guard: SQLSTATE %q (%v), want 25006 read_only_sql_transaction", got, err)
	}
	_, err = pool.Exec(ctx, sleep)
	if got := sqlstate(err); got != "57014" {
		t.Errorf("pg_sleep(6) under the guard: SQLSTATE %q (%v), want 57014 query_canceled", got, err)
	}

	// A role that does not exist fails on first use, so a wrong PG_ROLE
	// shows in /api/_health instead of leaving an unguarded session.
	bad, err := db.Open(ctx, pg.APIDSN, "no_such_role")
	if err != nil {
		t.Fatal(err)
	}
	defer bad.Close()
	if err := bad.Ping(ctx); err == nil || !strings.Contains(err.Error(), `role "no_such_role" does not exist`) {
		t.Errorf("Ping with a missing role = %v, want the AfterConnect error", err)
	} else if got := sqlstate(err); got != "" {
		// SQLSTATE 22023 is class 22, which the API answers as the visitor's
		// bad_param; a failed guard is the server's fault.
		t.Errorf("a failed guard exposes SQLSTATE %s; want the error wrapped without it", got)
	}
}
