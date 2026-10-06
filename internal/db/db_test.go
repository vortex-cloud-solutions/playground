package db_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/vortex-cloud-solutions/playground/internal/db"
)

func TestGuard(t *testing.T) {
	want := []string{
		`SET ROLE "playground_ro"`,
		"SET default_transaction_read_only = on",
		"SET statement_timeout = '5s'",
	}
	if got := db.Guard("playground_ro"); !reflect.DeepEqual(got, want) {
		t.Errorf("Guard = %q, want %q", got, want)
	}
	if got := db.Guard(`ro"; RESET ROLE; --`)[0]; got != `SET ROLE "ro""; RESET ROLE; --"` {
		t.Errorf("Guard did not quote the role: %s", got)
	}
}

func TestOpenRejects(t *testing.T) {
	tests := []struct{ name, dsn, role, want string }{
		{"empty dsn", "", "playground_ro", "PG_DSN is empty"},
		{"empty role", "postgres://api@localhost/playground", "", "PG_ROLE is empty"},
		{"malformed dsn", "postgres://api:s3cret@localhost:notaport/playground", "playground_ro", "PG_DSN is not a valid PostgreSQL connection string"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := db.Open(context.Background(), tt.dsn, tt.role)
			if err == nil || err.Error() != tt.want {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
			if strings.Contains(err.Error(), "s3cret") {
				t.Fatalf("error echoes the password: %v", err)
			}
		})
	}
}
