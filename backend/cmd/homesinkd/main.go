// Command homesinkd is the Homesink home-server daemon. This file is wiring
// only: parse config, build the logger and HTTP server, run until a signal,
// shut down cleanly. All behaviour lives in internal packages.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/FancyFunction/homesink/backend/internal/buildinfo"
	"github.com/FancyFunction/homesink/backend/internal/config"
	"github.com/FancyFunction/homesink/backend/internal/httpapi"
)

func main() {
	if err := run(os.Getenv, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "homesinkd: "+err.Error())
		os.Exit(1)
	}
}

// run loads configuration, starts the server, and blocks until SIGTERM/SIGINT
// triggers a graceful drain. It returns a non-nil error on any startup or
// shutdown failure; main turns that into a non-zero exit.
func run(getenv config.Getenv, stdout io.Writer) error {
	cfg, err := config.Load(getenv)
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewJSONHandler(stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)
	logger.LogAttrs(context.Background(), slog.LevelInfo, "starting homesinkd",
		append([]slog.Attr{slog.String("build", buildinfo.Get().String())}, cfg.LogAttrs()...)...)

	srv := httpapi.New(cfg, logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Start() }()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
		stop() // let a second signal kill the process immediately
		logger.Info("shutdown signal received, draining")
		shutdownErr := srv.Shutdown(context.Background())
		startErr := <-serveErr
		if startErr != nil {
			return startErr
		}
		if shutdownErr != nil {
			return shutdownErr
		}
		logger.Info("stopped cleanly")
		return nil
	}
}
