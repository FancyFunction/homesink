package store_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/FancyFunction/homesink/backend/internal/core"
	"github.com/FancyFunction/homesink/backend/internal/store"
	"github.com/FancyFunction/homesink/backend/internal/testutil"
)

// TestConcurrentInsertAndReadWithEightGoroutines is the WP-B2 acceptance
// criterion "concurrent 8-goroutine insert+read test passes with -race".
//
// Every goroutine writes blobs and items into the *same* album/year/month, so
// they all contend for one album_stats row — the write path multiple phones
// syncing at once actually hit (01-DECISIONS.md §12) — while also reading. What
// this proves: no SQLITE_BUSY escapes (single write connection plus
// busy_timeout), no data race (the -race detector), and the counters still equal
// the aggregate afterwards.
func TestConcurrentInsertAndReadWithEightGoroutines(t *testing.T) {
	const (
		goroutines   = 8
		perGoroutine = 25
	)

	ctx := context.Background()
	s := newSQLiteStore(t)

	var wg sync.WaitGroup
	errCh := make(chan error, goroutines*perGoroutine*2)

	for g := range goroutines {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range perGoroutine {
				hash := fmt.Sprintf("%056x%04d%04d", g, g, i)
				name := fmt.Sprintf("IMG_%d_%03d.jpg", g, i)

				blob := sampleBlob(hash, int64(1_000+i))
				blob.RelPath = "Camera/2025/12/" + name
				if err := s.InsertBlob(ctx, blob); err != nil {
					errCh <- fmt.Errorf("goroutine %d: insert blob: %w", g, err)
					return
				}
				item := sampleItem(fmt.Sprintf("itm_%d_%03d", g, i), hash, name,
					1_700_000_000_000+int64(g*1_000+i))
				if err := s.InsertItem(ctx, item); err != nil {
					errCh <- fmt.Errorf("goroutine %d: insert item: %w", g, err)
					return
				}

				// Read back through the read pool while other goroutines write.
				if _, err := s.BlobByHash(ctx, hash); err != nil {
					errCh <- fmt.Errorf("goroutine %d: read blob: %w", g, err)
					return
				}
				if _, err := s.ItemByPath(ctx, item.RelPath); err != nil {
					errCh <- fmt.Errorf("goroutine %d: read item: %w", g, err)
					return
				}
				if _, err := s.AlbumStat(ctx, "Camera", 2025, 12); err != nil {
					errCh <- fmt.Errorf("goroutine %d: read album stat: %w", g, err)
					return
				}
				if _, err := s.AlbumStats(ctx); err != nil {
					errCh <- fmt.Errorf("goroutine %d: list album stats: %w", g, err)
					return
				}
			}
		}(g)
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
	if t.Failed() {
		t.FailNow()
	}

	stat, err := s.AlbumStat(ctx, "Camera", 2025, 12)
	if err != nil {
		t.Fatalf("album stat: %v", err)
	}
	if want := int64(goroutines * perGoroutine); stat.ItemCount != want {
		t.Fatalf("item_count = %d, want %d", stat.ItemCount, want)
	}
	stored, err := s.AlbumStats(ctx)
	if err != nil {
		t.Fatalf("album stats: %v", err)
	}
	derived, err := s.DeriveAlbumStats(ctx)
	if err != nil {
		t.Fatalf("derive album stats: %v", err)
	}
	if !reflect.DeepEqual(stored, derived) {
		t.Fatalf("album_stats drifted under concurrency\n stored %+v\nderived %+v", stored, derived)
	}
}

// TestConcurrentInsertOfOneBlobHasExactlyOneWinner is the multi-device case of
// D-16: two phones uploading the same photo must not both create the blob. The
// loser sees store.ErrAlreadyExists, which the ingest path reports as a
// duplicate rather than a failure.
func TestConcurrentInsertOfOneBlobHasExactlyOneWinner(t *testing.T) {
	const goroutines = 8

	ctx := context.Background()
	s := newSQLiteStore(t)

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		winners int
	)
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := s.InsertBlob(ctx, sampleBlob(hashA, 4_096))
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				winners++
			case errors.Is(err, store.ErrAlreadyExists):
			default:
				t.Errorf("insert blob: unexpected error %v", err)
			}
		}()
	}
	wg.Wait()

	if winners != 1 {
		t.Fatalf("%d goroutines created the blob, want exactly 1", winners)
	}
}

// TestOpenRequiresAPath keeps the misconfiguration loud rather than creating a
// database somewhere surprising.
func TestOpenRequiresAPath(t *testing.T) {
	if _, err := store.Open(context.Background(), store.Options{}); err == nil {
		t.Fatal("open without a path succeeded, want an error")
	}
}

// TestOpenCreatesTheDatabaseDirectory: the daemon's first run has no
// .homesink/db yet.
func TestOpenCreatesTheDatabaseDirectory(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "nested", "db", "homesink.db")

	s, err := store.Open(ctx, store.Options{Path: path})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()
	if _, err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := s.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

// TestCloseTwiceIsSafe: shutdown paths close the store, and a second close on an
// error path must not panic.
func TestCloseTwiceIsSafe(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, store.Options{Path: filepath.Join(t.TempDir(), "homesink.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

// Both implementations must be usable wherever WP-B1's frozen contract is
// expected; the store and testutil packages assert this at compile time, and
// these declarations keep the test build honest about it too.
var (
	_ core.Store = (*store.SQLite)(nil)
	_ core.Store = (*testutil.MemStore)(nil)
)

// rawScalar reads one value straight out of the database file, bypassing the
// store, so a test can assert on columns the Go types do not expose — a NULL
// device_id, or blobs.state.
func rawScalar(t *testing.T, path, query string, args ...any) any {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=query_only(true)")
	if err != nil {
		t.Fatalf("open raw handle: %v", err)
	}
	defer func() { _ = db.Close() }()

	var value any
	if err := db.QueryRow(query, args...).Scan(&value); err != nil {
		t.Fatalf("raw query %q: %v", query, err)
	}
	return value
}
