package store_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/FancyFunction/homesink/backend/internal/store"
)

// TestMigrateEmptyDatabaseThenRerunIsNoOp is the WP-B2 acceptance criterion
// "Migration from empty DB and re-run are both no-ops the second time": the
// first Migrate builds the schema, and a second one applies nothing and leaves
// the schema byte-identical. This is what makes restarting the daemon — or an
// auto-update rollback putting the previous binary back (D-35) — safe.
func TestMigrateEmptyDatabaseThenRerunIsNoOp(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "db", "homesink.db")

	s, err := store.Open(ctx, store.Options{Path: path, BackupDir: filepath.Join(dir, "backups")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()

	if v, err := s.SchemaVersion(ctx); err != nil || v != 0 {
		t.Fatalf("schema version on an unmigrated database = %d, %v; want 0, nil", v, err)
	}

	applied, err := s.Migrate(ctx)
	if err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	if want := []int{1}; !reflect.DeepEqual(applied, want) {
		t.Fatalf("first migrate applied %v, want %v", applied, want)
	}
	if v, err := s.SchemaVersion(ctx); err != nil || v != 1 {
		t.Fatalf("schema version after migrating = %d, %v; want 1, nil", v, err)
	}
	firstSchema := schemaSnapshot(t, path)

	applied, err = s.Migrate(ctx)
	if err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if len(applied) != 0 {
		t.Fatalf("second migrate applied %v, want nothing", applied)
	}
	if v, err := s.SchemaVersion(ctx); err != nil || v != 1 {
		t.Fatalf("schema version after re-running = %d, %v; want 1, nil", v, err)
	}
	if second := schemaSnapshot(t, path); !reflect.DeepEqual(firstSchema, second) {
		t.Fatalf("re-running the migration changed the schema\nbefore %v\n after %v", firstSchema, second)
	}
}

// TestMigrateCreatesEveryTableAndIndex pins the object list from
// 03-DATA-MODEL.md §1.1, so a migration that silently drops one fails here.
func TestMigrateCreatesEveryTableAndIndex(t *testing.T) {
	s := newSQLiteStore(t)

	got := schemaSnapshot(t, s.Path())
	want := []string{
		"index:idx_devices_token",
		"index:idx_items_browse",
		"index:idx_items_hash",
		"index:idx_items_recent",
		"index:idx_jobs_claim",
		"index:idx_upload_sweep",
		"table:album_stats",
		"table:app_releases",
		"table:blob_variants",
		"table:blobs",
		"table:devices",
		"table:items",
		"table:jobs",
		"table:pairing_codes",
		"table:server_meta",
		"table:upload_sessions",
	}
	for _, object := range want {
		if !contains(got, object) {
			t.Errorf("missing schema object %s (have %v)", object, got)
		}
	}
}

// TestMigrateIsForwardOnly proves a database ahead of this binary is left alone.
// The auto-update rollback path (D-35) can put an older binary in front of a
// newer schema, and re-running or downgrading would corrupt it.
func TestMigrateIsForwardOnly(t *testing.T) {
	ctx := context.Background()
	s := newSQLiteStore(t)

	if err := s.SetMeta(ctx, store.MetaKeySchemaVersion, "99"); err != nil {
		t.Fatalf("set schema version: %v", err)
	}
	applied, err := s.Migrate(ctx)
	if err != nil {
		t.Fatalf("migrate against a newer schema: %v", err)
	}
	if len(applied) != 0 {
		t.Fatalf("applied %v against a newer schema, want nothing", applied)
	}
	if v, err := s.SchemaVersion(ctx); err != nil || v != 99 {
		t.Fatalf("schema version = %d, %v; want 99 kept, nil", v, err)
	}
}

// TestMigrateFreshDatabaseWritesNoBackup: the pre-migration backup (D-36) exists
// to protect data, and a database that has never been migrated holds none.
func TestMigrateFreshDatabaseWritesNoBackup(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	backups := filepath.Join(dir, "backups")

	s, err := store.Open(ctx, store.Options{
		Path:      filepath.Join(dir, "db", "homesink.db"),
		BackupDir: backups,
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()
	if _, err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	entries, err := os.ReadDir(backups)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read backup dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("fresh migration wrote backups %v, want none", entries)
	}
}

// TestOpenEnablesWALAndForeignKeys covers two of the four required pragmas
// through their effects: WAL leaves a -wal sidecar, and foreign_keys=ON makes
// items.device_id actually reject an unknown device.
func TestOpenEnablesWALAndForeignKeys(t *testing.T) {
	ctx := context.Background()
	s := newSQLiteStore(t)

	if err := s.SetMeta(ctx, "probe", "1"); err != nil {
		t.Fatalf("set meta: %v", err)
	}
	if _, err := os.Stat(s.Path() + "-wal"); err != nil {
		t.Fatalf("no WAL sidecar next to %s: %v", s.Path(), err)
	}

	mustInsertBlob(t, s, sampleBlob(hashA, 10))
	orphan := sampleItem("itm_1", hashA, "a.jpg", 1)
	orphan.DeviceID = "dev_does_not_exist"
	if err := s.InsertItem(ctx, orphan); err == nil {
		t.Fatal("inserting an item for an unknown device succeeded; foreign_keys is not ON")
	}
}

// TestMetaUnknownKeyIsNotFound documents the server_meta miss that WP-B3 sees
// before it has generated a TLS key.
func TestMetaUnknownKeyIsNotFound(t *testing.T) {
	s := newSQLiteStore(t)
	requireNotFound(t, errFrom(s.Meta(context.Background(), "tls_key_pem")))
}

// schemaSnapshot lists every table and index in the database, sorted, as
// "<type>:<name>". Comparing two snapshots is how the re-run test proves the
// second Migrate changed nothing.
func schemaSnapshot(t *testing.T, path string) []string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=query_only(true)")
	if err != nil {
		t.Fatalf("open snapshot handle: %v", err)
	}
	defer func() { _ = db.Close() }()

	rows, err := db.Query(
		`SELECT type, name FROM sqlite_master
		 WHERE name NOT LIKE 'sqlite_%' ORDER BY type, name`)
	if err != nil {
		t.Fatalf("read sqlite_master: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var kind, name string
		if err := rows.Scan(&kind, &name); err != nil {
			t.Fatalf("scan sqlite_master: %v", err)
		}
		out = append(out, kind+":"+name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read sqlite_master: %v", err)
	}
	return out
}

func contains(haystack []string, needle string) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}
