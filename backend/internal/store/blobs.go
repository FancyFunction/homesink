package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/FancyFunction/homesink/backend/internal/core"
)

// BlobState mirrors the blobs.state CHECK constraint.
type BlobState string

// Blob states. BlobMissing is set by homesinkd fsck when the backing file has
// vanished (invariant S6); a missing blob must never satisfy a dedupe hit (D-07).
const (
	BlobStored  BlobState = "stored"
	BlobMissing BlobState = "missing"
)

// VariantKind mirrors the blob_variants.kind CHECK constraint.
type VariantKind string

// Derived-file kinds (D-31, D-29).
const (
	VariantThumb256  VariantKind = "thumb256"
	VariantThumb1024 VariantKind = "thumb1024"
	VariantTranscode VariantKind = "transcode"
)

// BlobVariant is one derived file — a thumbnail or a transcode — keyed by
// content rather than by placement, so every item sharing a blob shares it.
type BlobVariant struct {
	Hash          string
	Kind          VariantKind
	RelPath       string
	SizeBytes     int64
	Width, Height int
	CreatedAt     int64
}

// blobColumns is the read projection, kept in one place so every scanner agrees.
const blobColumns = `hash, size_bytes, mime_type, media_type, rel_path, stored_hash,
	original_replaced_at, width, height, duration_ms, created_at`

// BlobByHash looks a blob up by the hash the client uploaded (D-08). An unknown
// hash — the normal preflight outcome for a new file — is core.ErrNotFound.
func (s *SQLite) BlobByHash(ctx context.Context, hash string) (*core.Blob, error) {
	row := s.read.QueryRowContext(ctx, `SELECT `+blobColumns+` FROM blobs WHERE hash = ?`, hash)
	b, err := scanBlob(row)
	if err != nil {
		return nil, notFound(err)
	}
	return b, nil
}

// InsertBlob records a blob. Per invariant S1 the caller only reaches this
// point after the file is verified and renamed into library/, so a row here
// means a real file on disk. A second insert of the same hash — two devices
// uploading one photo, D-16 — is ErrAlreadyExists, which the ingest path treats
// as a duplicate rather than a failure.
//
// State is not a parameter: a freshly committed blob is always 'stored'.
func (s *SQLite) InsertBlob(ctx context.Context, b core.Blob) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO blobs (hash, size_bytes, mime_type, media_type, rel_path, state,
				stored_hash, original_replaced_at, width, height, duration_ms, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			b.Hash, b.SizeBytes, b.MimeType, string(b.MediaType), b.RelPath, string(BlobStored),
			nullString(b.StoredHash), nullInt64Ptr(b.OriginalReplacedAt),
			nullInt(b.Width), nullInt(b.Height), nullInt64(b.DurationMs), b.CreatedAt)
		if err != nil {
			return fmt.Errorf("store: insert blob: %w", asAlreadyExists(err))
		}
		return nil
	})
}

// SetBlobState flips a blob between 'stored' and 'missing' (invariant S6).
func (s *SQLite) SetBlobState(ctx context.Context, hash string, state BlobState) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE blobs SET state = ? WHERE hash = ?`, string(state), hash)
		if err != nil {
			return fmt.Errorf("store: set blob state: %w", err)
		}
		return requireOneRow(res, "blob")
	})
}

// SetBlobStoredHash records the hash of the file as it is on disk now and stamps
// original_replaced_at. It is the transcoder's only write against blobs
// (D-08/invariant S4): blobs.hash stays the hash the client uploaded forever, or
// every already-synced video looks new to the next phone.
func (s *SQLite) SetBlobStoredHash(ctx context.Context, hash, storedHash string) error {
	replacedAt := s.now()
	return s.withTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE blobs SET stored_hash = ?, original_replaced_at = ? WHERE hash = ?`,
			storedHash, replacedAt, hash)
		if err != nil {
			return fmt.Errorf("store: set stored hash: %w", err)
		}
		return requireOneRow(res, "blob")
	})
}

// SetBlobMedia fills in the dimensions and duration an ffprobe job discovered.
func (s *SQLite) SetBlobMedia(ctx context.Context, hash string, width, height int, durationMs int64) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE blobs SET width = ?, height = ?, duration_ms = ? WHERE hash = ?`,
			nullInt(width), nullInt(height), nullInt64(durationMs), hash)
		if err != nil {
			return fmt.Errorf("store: set blob media: %w", err)
		}
		return requireOneRow(res, "blob")
	})
}

// InsertBlobVariant records a derived file. Per invariant S5 the caller writes
// the row only after the file has been fsync'd and renamed into place, so a row
// implies the file exists. Re-generating a variant replaces the row.
func (s *SQLite) InsertBlobVariant(ctx context.Context, v BlobVariant) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO blob_variants (hash, kind, rel_path, size_bytes, width, height, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT (hash, kind) DO UPDATE SET
				rel_path = excluded.rel_path, size_bytes = excluded.size_bytes,
				width = excluded.width, height = excluded.height, created_at = excluded.created_at`,
			v.Hash, string(v.Kind), v.RelPath, v.SizeBytes,
			nullInt(v.Width), nullInt(v.Height), v.CreatedAt)
		if err != nil {
			return fmt.Errorf("store: insert blob variant: %w", err)
		}
		return nil
	})
}

// BlobVariant returns one derived file, or core.ErrNotFound when it has not
// been generated yet — the 202 case of GET /v1/thumbs.
func (s *SQLite) BlobVariant(ctx context.Context, hash string, kind VariantKind) (*BlobVariant, error) {
	row := s.read.QueryRowContext(ctx,
		`SELECT hash, kind, rel_path, size_bytes, width, height, created_at
		 FROM blob_variants WHERE hash = ? AND kind = ?`, hash, string(kind))
	v, err := scanVariant(row)
	if err != nil {
		return nil, notFound(err)
	}
	return v, nil
}

// BlobVariantsByHash returns every derived file for a blob, ordered by kind.
func (s *SQLite) BlobVariantsByHash(ctx context.Context, hash string) ([]BlobVariant, error) {
	rows, err := s.read.QueryContext(ctx,
		`SELECT hash, kind, rel_path, size_bytes, width, height, created_at
		 FROM blob_variants WHERE hash = ? ORDER BY kind`, hash)
	if err != nil {
		return nil, fmt.Errorf("store: list blob variants: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []BlobVariant
	for rows.Next() {
		v, err := scanVariant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list blob variants: %w", err)
	}
	return out, nil
}

func scanBlob(sc scanner) (*core.Blob, error) {
	var (
		b          core.Blob
		mediaType  string
		storedHash sql.NullString
		replacedAt sql.NullInt64
		width      sql.NullInt64
		height     sql.NullInt64
		durationMs sql.NullInt64
	)
	if err := sc.Scan(&b.Hash, &b.SizeBytes, &b.MimeType, &mediaType, &b.RelPath,
		&storedHash, &replacedAt, &width, &height, &durationMs, &b.CreatedAt); err != nil {
		return nil, err
	}
	b.MediaType = core.MediaType(mediaType)
	b.StoredHash = storedHash.String
	if replacedAt.Valid {
		v := replacedAt.Int64
		b.OriginalReplacedAt = &v
	}
	b.Width = int(width.Int64)
	b.Height = int(height.Int64)
	b.DurationMs = durationMs.Int64
	return &b, nil
}

func scanVariant(sc scanner) (*BlobVariant, error) {
	var (
		v      BlobVariant
		kind   string
		width  sql.NullInt64
		height sql.NullInt64
	)
	if err := sc.Scan(&v.Hash, &kind, &v.RelPath, &v.SizeBytes, &width, &height, &v.CreatedAt); err != nil {
		return nil, err
	}
	v.Kind = VariantKind(kind)
	v.Width = int(width.Int64)
	v.Height = int(height.Int64)
	return &v, nil
}
