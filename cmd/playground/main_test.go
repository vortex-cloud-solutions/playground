package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vortex-cloud-solutions/playground/internal/pgtest"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestRunRejects(t *testing.T) {
	tests := []struct {
		name string
		args []string
		env  map[string]string
		want string
	}{
		{"no subcommand", nil, nil, "usage: playground serve"},
		{"unknown subcommand", []string{"nope"}, nil, `unknown subcommand "nope"`},
		{"serve without PG_DSN", []string{"serve"}, nil, "PG_DSN is required"},
		{"serve on a bad address", []string{"serve"}, map[string]string{"PG_DSN": "postgres://api@localhost/playground", "LISTEN_ADDR": "127.0.0.1:99999"}, "invalid port"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := run(context.Background(), tt.args, env(tt.env))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestServeConfigDefaults(t *testing.T) {
	cfg, err := serveConfigFrom(env(map[string]string{"PG_DSN": "postgres://api@db/playground"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Role != "playground_ro" || cfg.ListenAddr != ":8080" {
		t.Errorf("defaults = %+v, want role playground_ro on :8080", cfg)
	}
}

// serve, wired to a real server: it answers, then shuts down cleanly.
func TestServe(t *testing.T) {
	pg := pgtest.Start(t)
	pgtest.ApplySchema(t, pg, "hello")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, serveConfig{DSN: pg.APIDSN, Role: "playground_ro"}, ln) }()

	base := "http://" + ln.Addr().String()
	for _, path := range []string{"/api/_health", "/api/hello/greeting/1"} {
		resp, err := http.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("GET %s = %d %s", path, resp.StatusCode, body)
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("serve returned %v after shutdown, want nil", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("serve did not return within 15 s of cancel")
	}
}
