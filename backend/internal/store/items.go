package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/FancyFunction/homesink/backend/internal/core"
)

// itemColumns is the read projection, kept in one place so every scanner agrees.
const itemColumns = `item_id, hash, album, year, month, filename, rel_path,
	captured_at_ms, captured_offset_min, device_id, created_at`

// InsertItem records one placement of a blob and updates album_stats in the
// same transaction (invariant S3). A second insert at the same rel_path is
// ErrAlreadyExists; the D-13 collision suffix is the caller's job, so a
// duplicate here means the same file, not a name clash.
//
// it.DeviceID may be empty, which stores NULL — items.device_id has a foreign
// key to devices, so an empty string would be rejected.
func (s *SQLite) InsertItem(ctx context.Context, it core.Item) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		var sizeBytes int64
		err := tx.QueryRowContext(ctx, `SELECT size_bytes FROM blobs WHERE hash = ?`, it.Hash).Scan(&sizeBytes)
		if err != nil {
			return fmt.Errorf("store: insert item: unknown blob %q: %w", it.Hash, notFound(err))
		}

		_, err = tx.ExecContext(ctx,
			`INSERT INTO items (item_id, hash, album, year, month, filename, rel_path,
				captured_at_ms, captured_offset_min, device_id, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			it.ItemID, it.Hash, it.Album, it.Year, it.Month, it.Filename, it.RelPath,
			it.CapturedAtMs, it.CapturedOffsetMin, nullString(it.DeviceID), it.CreatedAt)
		if err != nil {
			return fmt.Errorf("store: insert item: %w", asAlreadyExists(err))
		}

		return addToAlbumStatsTx(ctx, tx, it.Album, it.Year, it.Month, sizeBytes, it.CapturedAtMs)
	})
}

// ItemByPath looks a placement up by its library-relative path. An unknown path
// is core.ErrNotFound.
func (s *SQLite) ItemByPath(ctx context.Context, relPath string) (*core.Item, error) {
	row := s.read.QueryRowContext(ctx, `SELECT `+itemColumns+` FROM items WHERE rel_path = ?`, relPath)
	it, err := scanItem(row)
	if err != nil {
		return nil, notFound(err)
	}
	return it, nil
}

// ItemByID looks a placement up by its item id. An unknown id is core.ErrNotFound.
func (s *SQLite) ItemByID(ctx context.Context, itemID string) (*core.Item, error) {
	row := s.read.QueryRowContext(ctx, `SELECT `+itemColumns+` FROM items WHERE item_id = ?`, itemID)
	it, err := scanItem(row)
	if err != nil {
		return nil, notFound(err)
	}
	return it, nil
}

// ItemsByHash returns every placement of one blob — the same photo in two
// albums or from two phones (D-06) — ordered by item id for a stable result.
func (s *SQLite) ItemsByHash(ctx context.Context, hash string) ([]core.Item, error) {
	rows, err := s.read.QueryContext(ctx,
		`SELECT `+itemColumns+` FROM items WHERE hash = ? ORDER BY item_id`, hash)
	if err != nil {
		return nil, fmt.Errorf("store: list items by hash: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []core.Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list items by hash: %w", err)
	}
	return out, nil
}

// DeleteItem removes one placement and decrements album_stats in the same
// transaction (invariant S3). The blob and its file are untouched: other items
// may still reference them. An unknown item id is core.ErrNotFound.
func (s *SQLite) DeleteItem(ctx context.Context, itemID string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		var (
			album        string
			year, month  int
			capturedAtMs int64
			sizeBytes    int64
		)
		err := tx.QueryRowContext(ctx,
			`SELECT i.album, i.year, i.month, i.captured_at_ms, b.size_bytes
			 FROM items i JOIN blobs b ON b.hash = i.hash WHERE i.item_id = ?`, itemID).
			Scan(&album, &year, &month, &capturedAtMs, &sizeBytes)
		if err != nil {
			return notFound(err)
		}

		if _, err := tx.ExecContext(ctx, `DELETE FROM items WHERE item_id = ?`, itemID); err != nil {
			return fmt.Errorf("store: delete item: %w", err)
		}

		return removeFromAlbumStatsTx(ctx, tx, album, year, month, sizeBytes)
	})
}

func scanItem(sc scanner) (*core.Item, error) {
	var (
		it       core.Item
		deviceID sql.NullString
	)
	if err := sc.Scan(&it.ItemID, &it.Hash, &it.Album, &it.Year, &it.Month, &it.Filename,
		&it.RelPath, &it.CapturedAtMs, &it.CapturedOffsetMin, &deviceID, &it.CreatedAt); err != nil {
		return nil, err
	}
	it.DeviceID = deviceID.String
	return &it, nil
}
