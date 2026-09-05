// Package store is the persistence layer: a SQLite implementation of the
// frozen core.Store contract plus the rest of the schema in
// 03-DATA-MODEL.md §1.1. Other packages depend on the Store interface below and
// are faked with testutil.MemStore in their tests; a new method here is a
// WP-B2 change request (08-ROADMAP.md §4).
//
// Timestamps. Every *_at / *_at_ms column in this package is Unix epoch
// milliseconds, matching core.Item.CapturedAtMs. Rows the caller constructs
// carry the caller's CreatedAt; state transitions the store performs itself
// (job claims, revocations, code use) are stamped from the injectable clock in
// Options.Now.
//
// Zero values and NULL. A nullable column round-trips through the Go zero
// value: an empty core.Blob.StoredHash, a zero Width/Height/DurationMs and an
// empty core.Item.DeviceID are all stored as NULL and read back as the zero
// value. That keeps items.device_id from tripping its foreign key when an item
// has no owning device.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/FancyFunction/homesink/backend/internal/core"

	// Pure-Go SQLite driver. A cgo driver would break the container build
	// (03-DATA-MODEL.md §1).
	sqlitedrv "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// ErrAlreadyExists is returned when an insert loses a race with an identical
// row: a second InsertBlob for the same hash, or a second InsertItem for the
// same rel_path. Callers that treat this as success — the concurrent-upload
// loser of D-16, which must see `duplicate` and not an error — check for it
// with errors.Is.
var ErrAlreadyExists = errors.New("store: row already exists")

// defaultKeepBackups is D-36: keep the seven most recent VACUUM INTO snapshots.
const defaultKeepBackups = 7

// Store is the whole persistence surface. core.Store is frozen after WP-B1, so
// the schema's remaining tables are reached through this superset instead.
// store.SQLite and testutil.MemStore both implement it and both must pass
// store/conformance_test.go.
type Store interface {
	core.Store

	// server_meta — schema_version, server_id, tls_key_pem, tls_spki.
	Meta(ctx context.Context, key string) (string, error)
	SetMeta(ctx context.Context, key, value string) error

	// blobs and blob_variants.
	SetBlobState(ctx context.Context, hash string, state BlobState) error
	SetBlobStoredHash(ctx context.Context, hash, storedHash string) error
	SetBlobMedia(ctx context.Context, hash string, width, height int, durationMs int64) error
	InsertBlobVariant(ctx context.Context, v BlobVariant) error
	BlobVariant(ctx context.Context, hash string, kind VariantKind) (*BlobVariant, error)
	BlobVariantsByHash(ctx context.Context, hash string) ([]BlobVariant, error)

	// items.
	ItemByID(ctx context.Context, itemID string) (*core.Item, error)
	ItemsByHash(ctx context.Context, hash string) ([]core.Item, error)
	DeleteItem(ctx context.Context, itemID string) error

	// album_stats.
	AlbumStat(ctx context.Context, album string, year, month int) (*AlbumStat, error)
	AlbumStats(ctx context.Context) ([]AlbumStat, error)
	DeriveAlbumStats(ctx context.Context) ([]AlbumStat, error)

	// jobs.
	EnqueueJob(ctx context.Context, j NewJob) (int64, error)
	ClaimJob(ctx context.Context) (*Job, error)
	CompleteJob(ctx context.Context, jobID int64) error
	FailJob(ctx context.Context, jobID int64, cause string, nextAttemptAtMs int64, maxAttempts int) error
	JobByID(ctx context.Context, jobID int64) (*Job, error)
	JobsByState(ctx context.Context, state JobState, limit int) ([]Job, error)
	RequeueRunningJobs(ctx context.Context) (int, error)

	// devices.
	InsertDevice(ctx context.Context, d Device) error
	DeviceByID(ctx context.Context, deviceID string) (*Device, error)
	DeviceByTokenHash(ctx context.Context, tokenHash string) (*Device, error)
	ListDevices(ctx context.Context) ([]Device, error)
	RevokeDevice(ctx context.Context, deviceID string) error
	TouchDevice(ctx context.Context, deviceID string, appVersionCode int64) error

	// pairing_codes.
	InsertPairingCode(ctx context.Context, pc PairingCode) error
	PairingCodeByHash(ctx context.Context, codeHash string) (*PairingCode, error)
	MarkPairingCodeUsed(ctx context.Context, codeHash string) error
	IncrementPairingCodeAttempts(ctx context.Context, codeHash string) (int, error)
	DeletePairingCodesBefore(ctx context.Context, expiresBeforeMs int64) (int, error)
}

// Options configures Open.
type Options struct {
	// Path is the database file, normally $HOMESINK_DATA/.homesink/db/homesink.db.
	// Its parent directory is created if missing.
	Path string
	// BackupDir is where VACUUM INTO snapshots go, normally
	// $HOMESINK_DATA/.homesink/backups. Backup fails if it is empty.
	BackupDir string
	// KeepBackups is how many snapshots to retain; 0 means defaultKeepBackups (D-36).
	KeepBackups int
	// ReadConns bounds the read-only pool; 0 means max(4, NumCPU).
	ReadConns int
	// Logger receives migration and backup events; nil discards them.
	Logger *slog.Logger
	// Now is the clock used for store-performed state transitions; nil means
	// time.Now. Tests inject a fixed clock.
	Now func() int64
}

// SQLite is the core.Store implementation backed by one database file.
//
// It holds two handles over that file. All writes go through write, capped at a
// single connection so this process never fights itself for the write lock and
// SQLITE_BUSY effectively disappears; reads go through a pool that WAL lets run
// concurrently with the writer.
type SQLite struct {
	write *sql.DB
	read  *sql.DB

	path        string
	backupDir   string
	keepBackups int
	log         *slog.Logger
	now         func() int64
}

// Compile-time proof that SQLite implements both the frozen contract and the
// package's own superset.
var (
	_ core.Store = (*SQLite)(nil)
	_ Store      = (*SQLite)(nil)
)

// Open opens (creating if absent) the database and both handles with the four
// pragmas from 03-DATA-MODEL.md §1. It does not migrate; call Migrate next.
func Open(ctx context.Context, opts Options) (*SQLite, error) {
	if opts.Path == "" {
		return nil, errors.New("store: Options.Path is required")
	}
	if err := os.MkdirAll(filepath.Dir(opts.Path), 0o750); err != nil {
		return nil, fmt.Errorf("store: create database directory: %w", err)
	}

	s := &SQLite{
		path:        opts.Path,
		backupDir:   opts.BackupDir,
		keepBackups: orDefaultInt(opts.KeepBackups, defaultKeepBackups),
		log:         opts.Logger,
		now:         opts.Now,
	}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	if s.now == nil {
		s.now = func() int64 { return time.Now().UnixMilli() }
	}

	write, err := sql.Open("sqlite", dsn(opts.Path, false))
	if err != nil {
		return nil, fmt.Errorf("store: open write handle: %w", err)
	}
	// One writer, one connection: the standard SQLite-under-concurrency shape.
	write.SetMaxOpenConns(1)
	write.SetMaxIdleConns(1)
	write.SetConnMaxLifetime(0)
	if err := write.PingContext(ctx); err != nil {
		_ = write.Close()
		return nil, fmt.Errorf("store: open %s: %w", opts.Path, err)
	}
	s.write = write

	read, err := sql.Open("sqlite", dsn(opts.Path, true))
	if err != nil {
		_ = write.Close()
		return nil, fmt.Errorf("store: open read handle: %w", err)
	}
	readConns := orDefaultInt(opts.ReadConns, max(4, runtime.NumCPU()))
	read.SetMaxOpenConns(readConns)
	read.SetMaxIdleConns(readConns)
	read.SetConnMaxLifetime(0)
	if err := read.PingContext(ctx); err != nil {
		_ = write.Close()
		_ = read.Close()
		return nil, fmt.Errorf("store: open %s read-only: %w", opts.Path, err)
	}
	s.read = read

	return s, nil
}

// Path returns the database file this store is backed by.
func (s *SQLite) Path() string { return s.path }

// Ping verifies both handles are usable. It is the DB half of the /healthz
// readiness check.
func (s *SQLite) Ping(ctx context.Context) error {
	if err := s.write.PingContext(ctx); err != nil {
		return fmt.Errorf("store: write handle: %w", err)
	}
	if err := s.read.PingContext(ctx); err != nil {
		return fmt.Errorf("store: read handle: %w", err)
	}
	return nil
}

// Close closes both handles. It is safe to call twice.
func (s *SQLite) Close() error {
	var errs []error
	if s.read != nil {
		if err := s.read.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close read handle: %w", err))
		}
		s.read = nil
	}
	if s.write != nil {
		if err := s.write.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close write handle: %w", err))
		}
		s.write = nil
	}
	return errors.Join(errs...)
}

// dsn builds the connection string. The four pragmas from
// 03-DATA-MODEL.md §1 are applied to every connection; the read handle adds
// query_only so a stray write is a loud error rather than a silent second
// writer. _txlock=immediate makes write transactions take the write lock up
// front, which is what turns a cross-process SQLITE_BUSY into a busy_timeout
// wait instead of an "upgrade failed" error.
func dsn(path string, readOnly bool) string {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(ON)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(NORMAL)")
	if readOnly {
		q.Add("_pragma", "query_only(true)")
	} else {
		q.Set("_txlock", "immediate")
	}
	return "file:" + path + "?" + q.Encode()
}

// withTx runs fn inside a single write transaction, rolling back on any error
// or panic. Every write in this package goes through it, which is how the
// album_stats update lands in the same transaction as its items write
// (invariant S3).
func (s *SQLite) withTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	committed = true
	return nil
}

// notFound maps sql.ErrNoRows onto the wire error so handlers can return it
// untouched, and leaves every other error alone.
func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return core.ErrNotFound()
	}
	return err
}

// asAlreadyExists maps a UNIQUE or PRIMARY KEY constraint violation onto
// ErrAlreadyExists and leaves every other error alone.
func asAlreadyExists(err error) error {
	if err == nil {
		return nil
	}
	if isUniqueViolation(err) {
		return ErrAlreadyExists
	}
	return err
}

// isUniqueViolation reports whether err is the driver's UNIQUE or PRIMARY KEY
// constraint failure. Matching on the extended result code rather than the
// message keeps this working when the driver rewords itself.
func isUniqueViolation(err error) bool {
	var derr *sqlitedrv.Error
	if !errors.As(err, &derr) {
		return false
	}
	switch derr.Code() {
	case sqlite3.SQLITE_CONSTRAINT_UNIQUE, sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY:
		return true
	default:
		return false
	}
}

func orDefaultInt(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}

// scanner is the shared shape of *sql.Row and *sql.Rows, so a row scanner can
// serve both a single-row query and a loop.
type scanner interface {
	Scan(dest ...any) error
}

// requireOneRow turns an UPDATE that matched nothing into core.ErrNotFound.
func requireOneRow(res sql.Result, what string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: rows affected for %s: %w", what, err)
	}
	if n == 0 {
		return core.ErrNotFound()
	}
	return nil
}

// nullString stores "" as NULL, so an unset optional column is NULL rather
// than an empty string.
func nullString(v string) any {
	if v == "" {
		return nil
	}
	return v
}

// nullInt stores 0 as NULL for optional integer columns (width, height).
func nullInt(v int) any {
	if v == 0 {
		return nil
	}
	return int64(v)
}

// nullInt64 stores 0 as NULL for optional integer columns (duration_ms).
func nullInt64(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

// nullInt64Ptr stores a nil pointer as NULL.
func nullInt64Ptr(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}
