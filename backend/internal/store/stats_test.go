package store_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"reflect"
	"testing"

	"github.com/FancyFunction/homesink/backend/internal/core"
	"github.com/FancyFunction/homesink/backend/internal/store"
)

// TestAlbumStatsMatchDerivedAggregateAfter1000RandomOps is the WP-B2 acceptance
// criterion "album_stats matches a re-derived aggregate after 1000 random
// insert/delete ops" — invariant S3. The denormalised counters exist only so the
// album list never runs a COUNT(*) (01-DECISIONS.md §11); the moment they drift
// from the truth, the browse screen lies. Both implementations are checked,
// because a fake that keeps different books is a bug that only shows up in
// production.
func TestAlbumStatsMatchDerivedAggregateAfter1000RandomOps(t *testing.T) {
	const ops = 1000

	for _, impl := range implementations() {
		t.Run(impl.name, func(t *testing.T) {
			ctx := context.Background()
			s := impl.open(t)

			// A handful of blobs of differing sizes, so size_bytes has to track
			// which blob each item points at.
			hashes := make([]string, 8)
			sizes := make(map[string]int64, len(hashes))
			for i := range hashes {
				h := fmt.Sprintf("%060x%04d", i, i)
				hashes[i] = h
				sizes[h] = int64(1_000 * (i + 1))
				b := sampleBlob(h, sizes[h])
				b.RelPath = fmt.Sprintf("Camera/2025/12/blob-%d.jpg", i)
				mustInsertBlob(t, s, b)
			}

			albums := []string{"Camera", "Screenshots", "WhatsApp Images", "Unsortiert"}
			rng := rand.New(rand.NewPCG(42, 1))

			var live []string // item ids currently in the store
			seq := 0
			for op := range ops {
				// Bias towards inserts so the store keeps growing rather than
				// hovering around empty, but delete often enough to exercise the
				// decrement and the latest-captured recompute.
				if len(live) > 0 && rng.IntN(100) < 35 {
					victim := rng.IntN(len(live))
					if err := s.DeleteItem(ctx, live[victim]); err != nil {
						t.Fatalf("op %d: delete item %s: %v", op, live[victim], err)
					}
					live = append(live[:victim], live[victim+1:]...)
				} else {
					seq++
					hash := hashes[rng.IntN(len(hashes))]
					album := albums[rng.IntN(len(albums))]
					year := 2024 + rng.IntN(3)
					month := 1 + rng.IntN(12)
					it := core.Item{
						ItemID:   fmt.Sprintf("itm_%016d", seq),
						Hash:     hash,
						Album:    album,
						Year:     year,
						Month:    month,
						Filename: fmt.Sprintf("IMG_%05d.jpg", seq),
						RelPath: fmt.Sprintf("%s/%d/%02d/IMG_%05d.jpg",
							album, year, month, seq),
						CapturedAtMs:      1_600_000_000_000 + int64(rng.IntN(100_000_000)),
						CapturedOffsetMin: 60,
						CreatedAt:         testNowMs,
					}
					if err := s.InsertItem(ctx, it); err != nil {
						t.Fatalf("op %d: insert item %s: %v", op, it.ItemID, err)
					}
					live = append(live, it.ItemID)
				}

				// Check every so often as well as at the end, so a failure points
				// at the operation that broke the invariant.
				if op%100 == 99 {
					assertStatsMatchDerived(t, ctx, s, op)
				}
			}

			assertStatsMatchDerived(t, ctx, s, ops-1)

			// And the counters really do describe the surviving items.
			var total int64
			stats, err := s.AlbumStats(ctx)
			if err != nil {
				t.Fatalf("album stats: %v", err)
			}
			for _, st := range stats {
				total += st.ItemCount
			}
			if total != int64(len(live)) {
				t.Fatalf("counters total %d items, %d are live", total, len(live))
			}
		})
	}
}

// TestAlbumStatsTrackTheNewestCaptureAcrossDeletes isolates the part of the
// bookkeeping that cannot be maintained by arithmetic: deleting the newest item
// in a month has to recompute latest_captured_at_ms, not decrement it.
func TestAlbumStatsTrackTheNewestCaptureAcrossDeletes(t *testing.T) {
	for _, impl := range implementations() {
		t.Run(impl.name, func(t *testing.T) {
			ctx := context.Background()
			s := impl.open(t)
			mustInsertBlob(t, s, sampleBlob(hashA, 500))

			captures := []int64{3_000, 1_000, 2_000}
			ids := make([]string, len(captures))
			for i, at := range captures {
				it := sampleItem(fmt.Sprintf("itm_%d", i), hashA, fmt.Sprintf("f%d.jpg", i), at)
				mustInsertItem(t, s, it)
				ids[i] = it.ItemID
			}

			if got := mustStat(t, ctx, s).LatestCapturedAtMs; got != 3_000 {
				t.Fatalf("latest = %d, want 3000", got)
			}
			if err := s.DeleteItem(ctx, ids[0]); err != nil { // the newest one
				t.Fatalf("delete: %v", err)
			}
			if got := mustStat(t, ctx, s).LatestCapturedAtMs; got != 2_000 {
				t.Fatalf("latest after deleting the newest = %d, want 2000", got)
			}
			if err := s.DeleteItem(ctx, ids[1]); err != nil { // the oldest one
				t.Fatalf("delete: %v", err)
			}
			if got := mustStat(t, ctx, s).LatestCapturedAtMs; got != 2_000 {
				t.Fatalf("latest after deleting the oldest = %d, want 2000 unchanged", got)
			}
		})
	}
}

func mustStat(t *testing.T, ctx context.Context, s store.Store) store.AlbumStat {
	t.Helper()
	st, err := s.AlbumStat(ctx, "Camera", 2025, 12)
	if err != nil {
		t.Fatalf("album stat: %v", err)
	}
	return *st
}

func assertStatsMatchDerived(t *testing.T, ctx context.Context, s store.Store, op int) {
	t.Helper()
	stored, err := s.AlbumStats(ctx)
	if err != nil {
		t.Fatalf("op %d: album stats: %v", op, err)
	}
	derived, err := s.DeriveAlbumStats(ctx)
	if err != nil {
		t.Fatalf("op %d: derive album stats: %v", op, err)
	}
	if !reflect.DeepEqual(stored, derived) {
		t.Fatalf("op %d: album_stats drifted from the aggregate over items\n stored %+v\nderived %+v",
			op, stored, derived)
	}
}
