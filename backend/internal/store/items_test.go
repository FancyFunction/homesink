package store_test

import (
	"context"
	"testing"
)

// TestInsertItemRejectsAnImpossibleMonth guards the month CHECK. Placement
// derives the month from the capture-local offset (D-11); a 0 or 13 here would
// mean a path like Camera/2025/00, which no file manager sorts sensibly.
func TestInsertItemRejectsAnImpossibleMonth(t *testing.T) {
	ctx := context.Background()
	s := newSQLiteStore(t)
	mustInsertBlob(t, s, sampleBlob(hashA, 10))

	for _, month := range []int{0, 13} {
		it := sampleItem("itm_1", hashA, "a.jpg", 1)
		it.Month = month
		it.RelPath = "Camera/2025/00/a.jpg"
		if err := s.InsertItem(ctx, it); err == nil {
			t.Errorf("month %d was accepted, want the CHECK to reject it", month)
		}
	}
}

// TestInsertItemDuplicateItemIDIsAlreadyExists: retrying a commit must not fan
// out into a second placement.
func TestInsertItemDuplicateItemIDIsAlreadyExists(t *testing.T) {
	ctx := context.Background()
	s := newSQLiteStore(t)
	mustInsertBlob(t, s, sampleBlob(hashA, 10))
	mustInsertItem(t, s, sampleItem("itm_1", hashA, "a.jpg", 1))

	dup := sampleItem("itm_1", hashA, "b.jpg", 2)
	requireAlreadyExists(t, s.InsertItem(ctx, dup))
}

// TestItemWithoutADeviceStoresNullNotEmptyString: items.device_id carries a
// foreign key, so an empty string would be rejected outright — and a NULL is the
// honest representation of "no device recorded" after ON DELETE SET NULL.
func TestItemWithoutADeviceStoresNullNotEmptyString(t *testing.T) {
	s := newSQLiteStore(t)
	mustInsertBlob(t, s, sampleBlob(hashA, 10))
	mustInsertItem(t, s, sampleItem("itm_1", hashA, "a.jpg", 1))

	got := rawScalar(t, s.Path(), `SELECT device_id IS NULL FROM items WHERE item_id = ?`, "itm_1")
	if got != int64(1) {
		t.Fatalf("items.device_id is not NULL for an item with no device")
	}
}

// TestFailedItemInsertLeavesAlbumStatsUntouched: the item write and the
// album_stats update share one transaction (invariant S3), so a rejected item
// must not leave a counter behind claiming it exists.
func TestFailedItemInsertLeavesAlbumStatsUntouched(t *testing.T) {
	ctx := context.Background()
	s := newSQLiteStore(t)
	mustInsertBlob(t, s, sampleBlob(hashA, 10))
	mustInsertItem(t, s, sampleItem("itm_1", hashA, "a.jpg", 1))

	dup := sampleItem("itm_2", hashA, "a.jpg", 2) // same rel_path
	requireAlreadyExists(t, s.InsertItem(ctx, dup))

	stat, err := s.AlbumStat(ctx, "Camera", 2025, 12)
	if err != nil {
		t.Fatalf("album stat: %v", err)
	}
	if stat.ItemCount != 1 || stat.SizeBytes != 10 {
		t.Fatalf("stat = %+v, want the rejected insert to have changed nothing", *stat)
	}
}
