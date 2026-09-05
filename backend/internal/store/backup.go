package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// backupPrefix and backupSuffix bracket a snapshot name:
// homesink-2026-09-05.db. The date is ISO so a lexical sort is a chronological
// sort, which is what Prune relies on.
const (
	backupPrefix = "homesink-"
	backupSuffix = ".db"
)

// Backup writes a consistent snapshot of the database with VACUUM INTO and then
// prunes the directory to the newest KeepBackups files (D-36). It returns the
// snapshot's path.
//
// VACUUM INTO is used rather than copying the file because it is consistent
// under WAL without stopping the server: losing the database loses albums,
// dedupe state and pairings, while the library files themselves survive anything.
//
// One snapshot per day: a same-day second call — the pre-migration backup landing
// on a day the nightly one already ran — is written to a temp file and renamed
// over the existing one, since VACUUM INTO refuses an existing target.
func (s *SQLite) Backup(ctx context.Context) (string, error) {
	if s.backupDir == "" {
		return "", errors.New("store: Backup needs Options.BackupDir")
	}
	if err := os.MkdirAll(s.backupDir, 0o750); err != nil {
		return "", fmt.Errorf("store: create backup directory: %w", err)
	}

	final := filepath.Join(s.backupDir, backupPrefix+
		time.UnixMilli(s.now()).UTC().Format("2006-01-02")+backupSuffix)
	tmp := final + ".tmp"
	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("store: clear stale backup temp file: %w", err)
	}

	// VACUUM INTO takes a literal, not a bound parameter, and it cannot run
	// inside a transaction.
	if _, err := s.write.ExecContext(ctx, `VACUUM INTO `+sqlQuote(tmp)); err != nil {
		return "", fmt.Errorf("store: vacuum into %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, final); err != nil {
		return "", fmt.Errorf("store: place backup: %w", err)
	}

	if _, err := s.PruneBackups(); err != nil {
		return final, err
	}
	s.log.InfoContext(ctx, "database backup written", "path", final)
	return final, nil
}

// PruneBackups deletes all but the newest KeepBackups snapshots and returns the
// paths it removed. Files that do not match the snapshot naming are left alone.
func (s *SQLite) PruneBackups() ([]string, error) {
	if s.backupDir == "" {
		return nil, errors.New("store: PruneBackups needs Options.BackupDir")
	}

	entries, err := os.ReadDir(s.backupDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("store: read backup directory: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, backupPrefix) && strings.HasSuffix(name, backupSuffix) {
			names = append(names, name)
		}
	}
	if len(names) <= s.keepBackups {
		return nil, nil
	}

	// ISO dates sort chronologically, so newest-first is a reverse sort.
	sort.Sort(sort.Reverse(sort.StringSlice(names)))

	var removed []string
	for _, name := range names[s.keepBackups:] {
		path := filepath.Join(s.backupDir, name)
		if err := os.Remove(path); err != nil {
			return removed, fmt.Errorf("store: prune backup %s: %w", path, err)
		}
		removed = append(removed, path)
	}
	return removed, nil
}

// sqlQuote renders a string as a SQL literal for the two statements that cannot
// take a bound parameter (VACUUM INTO). Doubling single quotes is the whole of
// SQLite's escaping rule for string literals.
func sqlQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
