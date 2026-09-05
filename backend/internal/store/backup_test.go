package store_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/FancyFunction/homesink/backend/internal/store"
)

// openWithClock builds a migrated store whose clock the test drives, so backups
// can be made to land on different days without waiting for one.
func openWithClock(t *testing.T, nowMs *int64, keep int) (*store.SQLite, string) {
	t.Helper()
	dir := t.TempDir()
	backupDir := filepath.Join(dir, "backups")
	s, err := store.Open(context.Background(), store.Options{
		Path:        filepath.Join(dir, "db", "homesink.db"),
		BackupDir:   backupDir,
		KeepBackups: keep,
		Now:         func() int64 { return *nowMs },
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if _, err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return s, backupDir
}

// TestBackupWritesAReadableSnapshot covers D-36: VACUUM INTO produces a
// consistent copy under WAL without stopping the server, and what it produces is
// a real database — losing the DB loses albums, dedupe state and pairings, so a
// backup that cannot be opened is worthless.
func TestBackupWritesAReadableSnapshot(t *testing.T) {
	ctx := context.Background()
	now := testNowMs
	s, backupDir := openWithClock(t, &now, 7)

	mustInsertBlob(t, s, sampleBlob(hashA, 1_234))
	mustInsertItem(t, s, sampleItem("itm_1", hashA, "a.jpg", 4_000))

	path, err := s.Backup(ctx)
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if want := filepath.Join(backupDir, "homesink-2026-01-01.db"); path != want {
		t.Fatalf("backup path = %q, want %q", path, want)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("the temp file survived the backup: %v", err)
	}

	restored, err := store.Open(ctx, store.Options{Path: path})
	if err != nil {
		t.Fatalf("open the snapshot: %v", err)
	}
	defer func() { _ = restored.Close() }()

	if v, err := restored.SchemaVersion(ctx); err != nil || v != 1 {
		t.Fatalf("snapshot schema version = %d, %v; want 1, nil", v, err)
	}
	item, err := restored.ItemByID(ctx, "itm_1")
	if err != nil {
		t.Fatalf("read the item back out of the snapshot: %v", err)
	}
	if item.RelPath != "Camera/2025/12/a.jpg" {
		t.Fatalf("snapshot item rel_path = %q", item.RelPath)
	}
}

// TestBackupTwiceOnOneDayReplacesTheSnapshot: the pre-migration backup can land
// on a day the nightly one already ran, and VACUUM INTO refuses an existing
// target — so the second one must overwrite rather than fail.
func TestBackupTwiceOnOneDayReplacesTheSnapshot(t *testing.T) {
	ctx := context.Background()
	now := testNowMs
	s, backupDir := openWithClock(t, &now, 7)

	first, err := s.Backup(ctx)
	if err != nil {
		t.Fatalf("first backup: %v", err)
	}
	mustInsertBlob(t, s, sampleBlob(hashA, 999))
	second, err := s.Backup(ctx)
	if err != nil {
		t.Fatalf("second backup: %v", err)
	}
	if first != second {
		t.Fatalf("same-day backups went to %q and %q, want one file", first, second)
	}
	if got := backupNames(t, backupDir); len(got) != 1 {
		t.Fatalf("backup directory holds %v, want one snapshot", got)
	}

	// The overwrite carries the newer content.
	restored, err := store.Open(ctx, store.Options{Path: second})
	if err != nil {
		t.Fatalf("open the snapshot: %v", err)
	}
	defer func() { _ = restored.Close() }()
	if _, err := restored.BlobByHash(ctx, hashA); err != nil {
		t.Fatalf("the second backup does not contain the newer blob: %v", err)
	}
}

// TestBackupKeepsOnlyTheSevenNewest is the retention half of D-36.
func TestBackupKeepsOnlyTheSevenNewest(t *testing.T) {
	ctx := context.Background()
	now := testNowMs
	s, backupDir := openWithClock(t, &now, 7)

	const day = int64(24 * 60 * 60 * 1000)
	for i := range 10 {
		now = testNowMs + int64(i)*day
		if _, err := s.Backup(ctx); err != nil {
			t.Fatalf("backup %d: %v", i, err)
		}
	}

	got := backupNames(t, backupDir)
	want := []string{
		"homesink-2026-01-04.db", "homesink-2026-01-05.db", "homesink-2026-01-06.db",
		"homesink-2026-01-07.db", "homesink-2026-01-08.db", "homesink-2026-01-09.db",
		"homesink-2026-01-10.db",
	}
	if len(got) != len(want) {
		t.Fatalf("kept %v, want the 7 newest %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("kept %v, want %v", got, want)
		}
	}
}

// TestPruneBackupsLeavesForeignFilesAlone: the backups directory is documented as
// machine-owned but a human may well have dropped something in it.
func TestPruneBackupsLeavesForeignFilesAlone(t *testing.T) {
	now := testNowMs
	s, backupDir := openWithClock(t, &now, 1)

	if _, err := s.Backup(context.Background()); err != nil {
		t.Fatalf("backup: %v", err)
	}
	stranger := filepath.Join(backupDir, "notes.txt")
	if err := os.WriteFile(stranger, []byte("do not delete"), 0o600); err != nil {
		t.Fatalf("write foreign file: %v", err)
	}

	now = testNowMs + 24*60*60*1000
	if _, err := s.Backup(context.Background()); err != nil {
		t.Fatalf("second backup: %v", err)
	}
	removed, err := s.PruneBackups()
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	for _, path := range removed {
		if path == stranger {
			t.Fatal("prune deleted a file it does not own")
		}
	}
	if _, err := os.Stat(stranger); err != nil {
		t.Fatalf("foreign file is gone: %v", err)
	}
	if got := backupNames(t, backupDir); len(got) != 1 || got[0] != "homesink-2026-01-02.db" {
		t.Fatalf("kept %v, want only the newest snapshot", got)
	}
}

// TestBackupWithoutABackupDirIsAnError: a store opened without one must say so
// rather than silently skipping the backup before a migration.
func TestBackupWithoutABackupDirIsAnError(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, store.Options{Path: filepath.Join(t.TempDir(), "homesink.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()

	if _, err := s.Backup(ctx); err == nil {
		t.Fatal("backup without a directory succeeded, want an error")
	}
	if _, err := s.PruneBackups(); err == nil {
		t.Fatal("prune without a directory succeeded, want an error")
	}
}

// backupNames lists the snapshot files, sorted, so retention is easy to assert.
func backupNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read backup dir: %v", err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".db" {
			out = append(out, e.Name())
		}
	}
	return out
}
