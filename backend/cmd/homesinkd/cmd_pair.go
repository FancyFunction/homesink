// This file implements the `homesinkd pair` subcommand: print a pairing code
// for someone standing at the server to type into their phone (D-01). Like
// main.go it is wiring only — the code is drawn and stored by internal/auth.
package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/FancyFunction/homesink/backend/internal/auth"
	"github.com/FancyFunction/homesink/backend/internal/config"
	"github.com/FancyFunction/homesink/backend/internal/store"
)

// pairCommand is the argument that selects this subcommand.
const pairCommand = "pair"

// runPair opens the database, issues a single-use pairing code and prints it.
// It is deliberately its own short-lived process: the daemon does not have to
// be running, and the code is valid for auth.DefaultCodeTTL either way because
// it lives in the database rather than in memory.
func runPair(ctx context.Context, getenv config.Getenv, stdout io.Writer) error {
	cfg, err := config.Load(getenv)
	if err != nil {
		return err
	}

	st, err := store.Open(ctx, store.Options{
		Path:      databasePath(cfg),
		BackupDir: filepath.Join(cfg.InternalDir(), "backups"),
	})
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	if _, err := st.Migrate(ctx); err != nil {
		return err
	}

	code, err := auth.NewPairer(st, auth.PairerOptions{}).IssueCode(ctx)
	if err != nil {
		return err
	}

	expires := time.UnixMilli(code.ExpiresAtMs).In(cfg.Location)
	_, err = fmt.Fprintf(stdout,
		"Kopplungscode: %s\nGültig bis:    %s (%s)\nDer Code kann genau einmal verwendet werden.\n",
		code.Value, expires.Format("15:04:05"), auth.DefaultCodeTTL.String())
	return err
}

// databasePath is $HOMESINK_DATA/.homesink/db/homesink.db
// (00-ARCHITECTURE.md §5).
func databasePath(cfg *config.Config) string {
	return filepath.Join(cfg.InternalDir(), "db", "homesink.db")
}
