// Package pgtest starts a throwaway PostGIS server for integration tests and
// prepares it the way the live project is prepared: the same three roles,
// and each module's schema.sql applied by `ingest`.
// A test that needs it fails, never skips, when Docker is unavailable.
package pgtest

import (
	"context"
	"io/fs"
	"net/url"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/vortex-cloud-solutions/playground/modules"
)

// Image is published for linux/amd64 only
// (https://hub.docker.com/r/postgis/postgis/tags?name=18-3.6), so an
// Apple-silicon host runs it under emulation.
const Image = "postgis/postgis:18-3.6"

// startup bounds the container's start. postgres.BasicWaitStrategies allows
// 60 s, which the PostGIS init scripts can exceed under amd64 emulation.
const startup = 3 * time.Minute

// DB holds one DSN per role.
type DB struct {
	// SuperDSN is the container's superuser.
	SuperDSN string
	// APIDSN logs in as api: a member of playground_ro and, as Vortex makes
	// every API-created role through neon_superuser, of pg_write_all_data.
	APIDSN string
	// IngestDSN logs in as ingest, which may create schemas.
	IngestDSN string
}

// Start runs a fresh server for this test and creates the live project's
// roles: playground_ro, api and ingest.
func Start(t testing.TB) DB {
	t.Helper()
	ctx := context.Background()
	ctr, err := postgres.Run(ctx, Image,
		testcontainers.WithImagePlatform("linux/amd64"),
		// The entrypoint starts Postgres twice: once to run the init
		// scripts, then for real. Wait for the second start and the port.
		testcontainers.WithWaitStrategyAndDeadline(startup,
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(startup),
			wait.ForListeningPort("5432/tcp").WithStartupTimeout(startup),
		),
	)
	testcontainers.CleanupContainer(t, ctr)
	if err != nil {
		t.Fatalf("start %s: %v", Image, err)
	}
	super, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	Exec(t, super,
		`CREATE ROLE playground_ro NOLOGIN`,
		`CREATE ROLE api LOGIN PASSWORD 'api'`,
		`GRANT playground_ro TO api`,
		`GRANT pg_write_all_data TO api`,
		`CREATE ROLE ingest LOGIN PASSWORD 'ingest'`,
		`GRANT CREATE ON DATABASE postgres TO ingest`,
	)
	return DB{
		SuperDSN:  super,
		APIDSN:    withUser(t, super, "api", "api"),
		IngestDSN: withUser(t, super, "ingest", "ingest"),
	}
}

// ApplySchema runs modules/<module>/schema.sql as ingest twice; the second
// run fails the test if the file is not idempotent.
func ApplySchema(t testing.TB, db DB, module string) {
	t.Helper()
	src, err := fs.ReadFile(modules.FS, module+"/schema.sql")
	if err != nil {
		t.Fatalf("read %s/schema.sql: %v", module, err)
	}
	Exec(t, db.IngestDSN, string(src), string(src))
}

// Exec runs each statement on one connection, over the simple protocol, so
// a statement may hold several SQL commands.
func Exec(t testing.TB, dsn string, stmts ...string) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)
	for _, s := range stmts {
		if _, err := conn.Exec(ctx, s); err != nil {
			t.Fatalf("exec %q: %v", s, err)
		}
	}
}

func withUser(t testing.TB, dsn, user, password string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse %s: %v", Image, err)
	}
	u.User = url.UserPassword(user, password)
	return u.String()
}
