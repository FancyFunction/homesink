package store

import (
	"context"
	"database/sql"
	"fmt"
)

// AlbumStat is one denormalised (album, year, month) counter row. It exists so
// the album list never pays for a COUNT(*) at read time — the ≤100 ms at 100k
// items budget in 01-DECISIONS.md §11.
type AlbumStat struct {
	Album              string
	Year, Month        int
	ItemCount          int64
	SizeBytes          int64
	LatestCapturedAtMs int64
}

// statColumns is the read projection, kept in one place so every scanner agrees.
const statColumns = `album, year, month, item_count, size_bytes, latest_captured_at_ms`

// AlbumStat returns one counter row, or core.ErrNotFound when that album/month
// holds no items.
func (s *SQLite) AlbumStat(ctx context.Context, album string, year, month int) (*AlbumStat, error) {
	row := s.read.QueryRowContext(ctx,
		`SELECT `+statColumns+` FROM album_stats WHERE album = ? AND year = ? AND month = ?`,
		album, year, month)
	st, err := scanAlbumStat(row)
	if err != nil {
		return nil, notFound(err)
	}
	return st, nil
}

// AlbumStats returns every counter row, newest month first within an album.
func (s *SQLite) AlbumStats(ctx context.Context) ([]AlbumStat, error) {
	return queryAlbumStats(ctx, s.read,
		`SELECT `+statColumns+` FROM album_stats ORDER BY album, year DESC, month DESC`)
}

// DeriveAlbumStats recomputes the same aggregate straight from items and blobs,
// ignoring album_stats entirely. It is what homesinkd fsck compares against to
// prove invariant S3, and what the WP-B2 acceptance test asserts equality with
// after a thousand random inserts and deletes.
func (s *SQLite) DeriveAlbumStats(ctx context.Context) ([]AlbumStat, error) {
	return queryAlbumStats(ctx, s.read,
		`SELECT i.album, i.year, i.month, COUNT(*), COALESCE(SUM(b.size_bytes), 0),
			COALESCE(MAX(i.captured_at_ms), 0)
		 FROM items i JOIN blobs b ON b.hash = i.hash
		 GROUP BY i.album, i.year, i.month
		 ORDER BY i.album, i.year DESC, i.month DESC`)
}

func queryAlbumStats(ctx context.Context, db *sql.DB, query string) ([]AlbumStat, error) {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("store: album stats: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []AlbumStat
	for rows.Next() {
		st, err := scanAlbumStat(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *st)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: album stats: %w", err)
	}
	return out, nil
}

// addToAlbumStatsTx folds one new item into its counter row. It runs inside the
// caller's items transaction, which is what keeps album_stats equal to the
// aggregate over items at every commit boundary (invariant S3).
func addToAlbumStatsTx(ctx context.Context, tx *sql.Tx, album string, year, month int,
	sizeBytes, capturedAtMs int64) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO album_stats (album, year, month, item_count, size_bytes, latest_captured_at_ms)
		 VALUES (?, ?, ?, 1, ?, ?)
		 ON CONFLICT (album, year, month) DO UPDATE SET
			item_count = item_count + 1,
			size_bytes = size_bytes + excluded.size_bytes,
			latest_captured_at_ms = MAX(
				COALESCE(album_stats.latest_captured_at_ms, excluded.latest_captured_at_ms),
				excluded.latest_captured_at_ms)`,
		album, year, month, sizeBytes, capturedAtMs)
	if err != nil {
		return fmt.Errorf("store: album stats increment: %w", err)
	}
	return nil
}

// removeFromAlbumStatsTx folds one deleted item out of its counter row, deleting
// the row when the last item goes. latest_captured_at_ms has to be recomputed
// rather than decremented — the item removed may have been the newest one.
func removeFromAlbumStatsTx(ctx context.Context, tx *sql.Tx, album string, year, month int,
	sizeBytes int64) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE album_stats
		 SET item_count = item_count - 1,
			 size_bytes = size_bytes - ?,
			 latest_captured_at_ms = (
				SELECT MAX(captured_at_ms) FROM items
				WHERE album = ? AND year = ? AND month = ?)
		 WHERE album = ? AND year = ? AND month = ?`,
		sizeBytes, album, year, month, album, year, month)
	if err != nil {
		return fmt.Errorf("store: album stats decrement: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM album_stats WHERE album = ? AND year = ? AND month = ? AND item_count <= 0`,
		album, year, month); err != nil {
		return fmt.Errorf("store: album stats prune: %w", err)
	}
	return nil
}

func scanAlbumStat(sc scanner) (*AlbumStat, error) {
	var (
		st     AlbumStat
		latest sql.NullInt64
	)
	if err := sc.Scan(&st.Album, &st.Year, &st.Month, &st.ItemCount, &st.SizeBytes, &latest); err != nil {
		return nil, err
	}
	st.LatestCapturedAtMs = latest.Int64
	return &st, nil
}
