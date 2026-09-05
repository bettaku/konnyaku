package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	migrations "konnyaku/db"
	"konnyaku/internal/config"
	"konnyaku/internal/db"
	"konnyaku/internal/server"
)

func main() {
	if err := run(); err != nil {
		slog.Error("konnyaku stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pc, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return errors.New("invalid DATABASE_URL")
	}
	pc.MaxConns = 12
	pc.ConnConfig.ConnectTimeout = 5 * time.Second
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err = pool.Ping(ctx); err != nil {
		return errors.New("cannot connect to PostgreSQL")
	}
	var version int
	if err = pool.QueryRow(ctx, "SELECT current_setting('server_version_num')::int").Scan(&version); err != nil {
		return err
	}
	if version < 180000 || version >= 190000 {
		return fmt.Errorf("PostgreSQL 18 required, got %d", version)
	}
	command := "serve"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	switch command {
	case "migrate":
		return migrations.Apply(ctx, pool)
	case "create-admin":
		_, err = server.CreateUser(ctx, db.New(pool), os.Getenv("ADMIN_EMAIL"), os.Getenv("ADMIN_PASSWORD"), os.Getenv("ADMIN_NAME"), true)
		if err == nil {
			slog.Info("administrator created")
		}
		return err
	case "serve":
	default:
		return errors.New("usage: konnyaku [serve|migrate|create-admin]")
	}
	var applied bool
	if err = pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version='001_initial.sql')").Scan(&applied); err != nil || !applied {
		return errors.New("run konnyaku migrate before serving")
	}
	s := server.New(pool, cfg)
	httpServer := &http.Server{Addr: cfg.Address, Handler: s.Echo, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 4 * time.Minute, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
	workerDone := make(chan struct{})
	go func() { defer close(workerDone); s.RunWorker(ctx) }()
	errCh := make(chan error, 1)
	go func() { slog.Info("listening", "address", cfg.Address); errCh <- httpServer.ListenAndServe() }()
	select {
	case err = <-errCh:
		stop()
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	shutdownErr := httpServer.Shutdown(shutdown)
	if shutdownErr != nil {
		_ = httpServer.Close()
	}
	<-workerDone
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return shutdownErr
}
