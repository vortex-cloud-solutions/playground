// Command playground serves the named-query API.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/vortex-cloud-solutions/playground/internal/db"
	"github.com/vortex-cloud-solutions/playground/internal/query"
	"github.com/vortex-cloud-solutions/playground/internal/server"
	"github.com/vortex-cloud-solutions/playground/modules"
)

const usage = "usage: playground serve"

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Getenv); err != nil {
		slog.Error("playground failed", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, getenv func(string) string) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	switch args[0] {
	case "serve":
		cfg, err := serveConfigFrom(getenv)
		if err != nil {
			return err
		}
		ln, err := net.Listen("tcp", cfg.ListenAddr)
		if err != nil {
			return err
		}
		return serve(ctx, cfg, ln)
	default:
		return fmt.Errorf("unknown subcommand %q; %s", args[0], usage)
	}
}

type serveConfig struct {
	DSN, Role, ListenAddr string
}

func serveConfigFrom(getenv func(string) string) (serveConfig, error) {
	cfg := serveConfig{DSN: getenv("PG_DSN"), Role: getenv("PG_ROLE"), ListenAddr: getenv("LISTEN_ADDR")}
	if cfg.DSN == "" {
		return serveConfig{}, errors.New("PG_DSN is required")
	}
	if cfg.Role == "" {
		cfg.Role = "playground_ro"
	}
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ":8080"
	}
	return cfg, nil
}

// serve answers on ln until ctx ends, then drains for up to 10 s.
func serve(ctx context.Context, cfg serveConfig, ln net.Listener) error {
	reg, err := query.Load(modules.FS)
	if err != nil {
		return fmt.Errorf("load queries: %w", err)
	}
	pool, err := db.Open(ctx, cfg.DSN, cfg.Role)
	if err != nil {
		return err
	}
	defer pool.Close()
	srv := &http.Server{
		Handler:           server.New(reg, pool),
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      40 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	slog.Info("serving", "addr", ln.Addr().String(), "queries", len(reg.All()), "role", cfg.Role)
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdown)
	}
}
