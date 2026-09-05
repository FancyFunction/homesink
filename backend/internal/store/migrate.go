package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/FancyFunction/homesink/backend/migrations"
)

// MetaKeySchemaVersion is the server_meta row holding the highest applied
// migration version (03-DATA-MODEL.md §1.3).
const MetaKeySchemaVersion = "schema_version"

// Migrate applies every embedded migration newer than the recorded schema
// version and returns the versions it applied, in order. A second call on an
// up-to-date database applies nothing and returns an empty slice.
//
// Each migration runs in its own transaction together with the schema_version
// bump, so a failure leaves the database at the previous version rather than
// half-migrated. Migrations are forward-only: a version below the recorded one
// is never re-run, and the recorded version is never lowered — the auto-update
// rollback path (D-35) can put the previous binary in front of this schema at
// any moment, which is exactly why a migration may not drop or retype a column
// (03-DATA-MODEL.md §1.3).
//
// An existing database is backed up before the first migration is applied
// (D-36); a fresh one is not, as there is nothing yet to lose. A backup failure
// aborts the migration.
func (s *SQLite) Migrate(ctx context.Context) ([]int, error) {
	all, err := migrations.All()
	if err != nil {
		return nil, err
	}

	current, err := s.SchemaVersion(ctx)
	if err != nil {
		return nil, err
	}

	pending := make([]migrations.Migration, 0, len(all))
	for _, m := range all {
		if m.Version > current {
			pending = append(pending, m)
		}
	}
	if len(pending) == 0 {
		s.log.DebugContext(ctx, "schema up to date", "schemaVersion", current)
		return nil, nil
	}

	if current > 0 && s.backupDir != "" {
		path, err := s.Backup(ctx)
		if err != nil {
			return nil, fmt.Errorf("store: pre-migration backup: %w", err)
		}
		s.log.InfoContext(ctx, "pre-migration backup written", "path", path, "schemaVersion", current)
	}

	applied := make([]int, 0, len(pending))
	for _, m := range pending {
		if err := s.applyOne(ctx, m); err != nil {
			return applied, err
		}
		applied = append(applied, m.Version)
		s.log.InfoContext(ctx, "migration applied", "version", m.Version, "name", m.Name)
	}
	return applied, nil
}

// applyOne runs one migration and records its version in the same transaction.
func (s *SQLite) applyOne(ctx context.Context, m migrations.Migration) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
			return fmt.Errorf("store: migration %s: %w", m.Name, err)
		}
		if err := setMetaTx(ctx, tx, MetaKeySchemaVersion, strconv.Itoa(m.Version)); err != nil {
			return fmt.Errorf("store: record schema version %d: %w", m.Version, err)
		}
		return nil
	})
}

// SchemaVersion returns the highest applied migration version, or 0 for a
// database that has never been migrated (server_meta does not exist yet).
func (s *SQLite) SchemaVersion(ctx context.Context) (int, error) {
	exists, err := s.tableExists(ctx, "server_meta")
	if err != nil {
		return 0, err
	}
	if !exists {
		return 0, nil
	}

	var raw string
	err = s.read.QueryRowContext(ctx,
		`SELECT value FROM server_meta WHERE key = ?`, MetaKeySchemaVersion).Scan(&raw)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 0, nil
	case err != nil:
		return 0, fmt.Errorf("store: read schema version: %w", err)
	}
	version, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("store: schema version %q is not an integer: %w", raw, err)
	}
	return version, nil
}

func (s *SQLite) tableExists(ctx context.Context, name string) (bool, error) {
	var found string
	err := s.read.QueryRowContext(ctx,
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&found)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("store: look up table %q: %w", name, err)
	}
	return true, nil
}

// Meta reads one server_meta value. A missing key is core.ErrNotFound.
func (s *SQLite) Meta(ctx context.Context, key string) (string, error) {
	var value string
	err := s.read.QueryRowContext(ctx, `SELECT value FROM server_meta WHERE key = ?`, key).Scan(&value)
	if err != nil {
		return "", notFound(err)
	}
	return value, nil
}

// SetMeta writes one server_meta value, replacing any previous one.
func (s *SQLite) SetMeta(ctx context.Context, key, value string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error { return setMetaTx(ctx, tx, key, value) })
}

func setMetaTx(ctx context.Context, tx *sql.Tx, key, value string) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO server_meta (key, value) VALUES (?, ?)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)
	if err != nil {
		return fmt.Errorf("store: set meta %q: %w", key, err)
	}
	return nil
}
