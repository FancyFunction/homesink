// conformance_test.go is the shared suite required by WP-B2: written once, run
// against store.SQLite and against testutil.MemStore. Every behaviour another
// package is allowed to rely on belongs here, because a fake that disagrees with
// the database turns green unit tests into a production bug.
//
// These tests live in package store_test rather than store: testutil imports
// store, so an in-package test importing testutil would be an import cycle.
package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/FancyFunction/homesink/backend/internal/core"
	"github.com/FancyFunction/homesink/backend/internal/store"
	"github.com/FancyFunction/homesink/backend/internal/testutil"
)

// testNowMs is the frozen clock both implementations run on, so timestamps the
// store stamps itself can be asserted exactly.
const testNowMs int64 = 1767225600000 // 2026-01-01T00:00:00Z

// newSQLiteStore opens a migrated database in a temp directory.
func newSQLiteStore(t *testing.T) *store.SQLite {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(context.Background(), store.Options{
		Path:      filepath.Join(dir, "db", "homesink.db"),
		BackupDir: filepath.Join(dir, "backups"),
		Now:       func() int64 { return testNowMs },
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if _, err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return s
}

func newMemStore(t *testing.T) *testutil.MemStore {
	t.Helper()
	m := testutil.NewMemStore()
	m.Now = func() int64 { return testNowMs }
	return m
}

// implementations is the pair every conformance case runs against.
func implementations() []struct {
	name string
	open func(t *testing.T) store.Store
} {
	return []struct {
		name string
		open func(t *testing.T) store.Store
	}{
		{"SQLite", func(t *testing.T) store.Store { return newSQLiteStore(t) }},
		{"MemStore", func(t *testing.T) store.Store { return newMemStore(t) }},
	}
}

// TestConformanceSQLiteAndMemStore is the WP-B2 acceptance criterion "MemStore
// and SQLite pass the same shared test suite".
func TestConformanceSQLiteAndMemStore(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T, s store.Store)
	}{
		{"MetaRoundTrip", conformMetaRoundTrip},
		{"BlobRoundTrip", conformBlobRoundTrip},
		{"BlobOptionalFieldsAreZeroValues", conformBlobOptionalFields},
		{"InsertBlobTwiceIsAlreadyExists", conformInsertBlobTwice},
		{"UnknownBlobIsNotFound", conformUnknownBlob},
		{"SetBlobStoredHashNeverRewritesHash", conformSetBlobStoredHash},
		{"SetBlobStateAndMedia", conformSetBlobStateAndMedia},
		{"BlobVariantUpsertAndList", conformBlobVariants},
		{"ItemRoundTrip", conformItemRoundTrip},
		{"ItemWithoutDeviceHasEmptyDeviceID", conformItemWithoutDevice},
		{"InsertItemUnknownBlobIsNotFound", conformInsertItemUnknownBlob},
		{"InsertItemDuplicatePathIsAlreadyExists", conformInsertItemDuplicatePath},
		{"ItemsByHashReturnsEveryPlacement", conformItemsByHash},
		{"DeleteItemDecrementsStatsAndRecomputesLatest", conformDeleteItemStats},
		{"DeleteLastItemRemovesStatRow", conformDeleteLastItem},
		{"AlbumStatsEqualDerivedAggregate", conformAlbumStatsEqualDerived},
		{"EnqueueAndClaimJobRespectsPriority", conformJobPriority},
		{"ClaimJobNotFoundWhenNothingDue", conformClaimNothingDue},
		{"FailJobRequeuesThenFailsAtMaxAttempts", conformFailJob},
		{"CompleteJobClearsLastError", conformCompleteJob},
		{"RequeueRunningJobsResetsClaimedJobs", conformRequeueRunningJobs},
		{"JobsByStateOrdersByIDAndHonoursLimit", conformJobsByState},
		{"DeviceRoundTripAndTokenLookup", conformDeviceRoundTrip},
		{"InsertDeviceTwiceIsAlreadyExists", conformInsertDeviceTwice},
		{"RevokedDeviceIsNotFoundByTokenHash", conformRevokedDevice},
		{"TouchDeviceUpdatesLastSeenAndVersion", conformTouchDevice},
		{"ListDevicesIncludesRevokedOldestFirst", conformListDevices},
		{"PairingCodeRoundTrip", conformPairingCodeRoundTrip},
		{"PairingCodeIsSingleUse", conformPairingCodeSingleUse},
		{"PairingCodeAttemptsIncrement", conformPairingCodeAttempts},
		{"DeletePairingCodesBeforeRemovesExpiredOnly", conformDeletePairingCodes},
	}

	for _, impl := range implementations() {
		t.Run(impl.name, func(t *testing.T) {
			for _, c := range cases {
				t.Run(c.name, func(t *testing.T) { c.run(t, impl.open(t)) })
			}
		})
	}
}

// --- helpers -----------------------------------------------------------------

func requireNotFound(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, core.ErrNotFound()) {
		t.Fatalf("want core.ErrNotFound, got %v", err)
	}
}

func requireAlreadyExists(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, store.ErrAlreadyExists) {
		t.Fatalf("want store.ErrAlreadyExists, got %v", err)
	}
}

func mustInsertBlob(t *testing.T, s store.Store, b core.Blob) core.Blob {
	t.Helper()
	if err := s.InsertBlob(context.Background(), b); err != nil {
		t.Fatalf("insert blob %s: %v", b.Hash, err)
	}
	return b
}

func mustInsertItem(t *testing.T, s store.Store, it core.Item) core.Item {
	t.Helper()
	if err := s.InsertItem(context.Background(), it); err != nil {
		t.Fatalf("insert item %s: %v", it.ItemID, err)
	}
	return it
}

// sampleBlob is a fully-populated image blob. hash is 64 hex chars like a real
// SHA-256 so nothing accidentally depends on a short key.
func sampleBlob(hash string, sizeBytes int64) core.Blob {
	return core.Blob{
		Hash:      hash,
		SizeBytes: sizeBytes,
		MimeType:  "image/jpeg",
		MediaType: core.MediaImage,
		RelPath:   "Camera/2025/12/IMG_0001.jpg",
		Width:     4032,
		Height:    3024,
		CreatedAt: testNowMs,
	}
}

// sampleItem places blob hash at Camera/2025/12/<filename>.
func sampleItem(itemID, hash, filename string, capturedAtMs int64) core.Item {
	return core.Item{
		ItemID:            itemID,
		Hash:              hash,
		Album:             "Camera",
		Year:              2025,
		Month:             12,
		Filename:          filename,
		RelPath:           "Camera/2025/12/" + filename,
		CapturedAtMs:      capturedAtMs,
		CapturedOffsetMin: 60,
		CreatedAt:         testNowMs,
	}
}

// samplePairingCode is a code hash with a live 10-minute TTL (D-01).
func samplePairingCode() store.PairingCode {
	return store.PairingCode{CodeHash: "sha256-of-123456", ExpiresAtMs: testNowMs + 600_000}
}

func sampleDevice(deviceID, tokenHash string) store.Device {
	return store.Device{
		DeviceID:       deviceID,
		Name:           "Pixel 8",
		TokenHash:      tokenHash,
		Platform:       "android",
		AppVersionCode: 12,
		CreatedAt:      testNowMs,
	}
}

const (
	hashA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	hashB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// --- cases -------------------------------------------------------------------

func conformMetaRoundTrip(t *testing.T, s store.Store) {
	ctx := context.Background()
	if _, err := s.Meta(ctx, "server_id"); !errors.Is(err, core.ErrNotFound()) {
		t.Fatalf("missing key: want core.ErrNotFound, got %v", err)
	}
	if err := s.SetMeta(ctx, "server_id", "srv_1"); err != nil {
		t.Fatalf("set meta: %v", err)
	}
	if err := s.SetMeta(ctx, "server_id", "srv_2"); err != nil {
		t.Fatalf("overwrite meta: %v", err)
	}
	got, err := s.Meta(ctx, "server_id")
	if err != nil {
		t.Fatalf("meta: %v", err)
	}
	if got != "srv_2" {
		t.Fatalf("meta = %q, want %q", got, "srv_2")
	}
}

func conformBlobRoundTrip(t *testing.T, s store.Store) {
	ctx := context.Background()
	want := sampleBlob(hashA, 2_500_000)
	want.DurationMs = 0
	mustInsertBlob(t, s, want)

	got, err := s.BlobByHash(ctx, hashA)
	if err != nil {
		t.Fatalf("blob by hash: %v", err)
	}
	if !reflect.DeepEqual(*got, want) {
		t.Fatalf("blob round trip\n got %+v\nwant %+v", *got, want)
	}
}

func conformBlobOptionalFields(t *testing.T, s store.Store) {
	ctx := context.Background()
	want := core.Blob{
		Hash:      hashB,
		SizeBytes: 17,
		MimeType:  "audio/mp4",
		MediaType: core.MediaAudio,
		RelPath:   "Unsortiert/2025/01/rec.m4a",
		CreatedAt: testNowMs,
	}
	mustInsertBlob(t, s, want)

	got, err := s.BlobByHash(ctx, hashB)
	if err != nil {
		t.Fatalf("blob by hash: %v", err)
	}
	if !reflect.DeepEqual(*got, want) {
		t.Fatalf("optional fields\n got %+v\nwant %+v", *got, want)
	}
}

func conformInsertBlobTwice(t *testing.T, s store.Store) {
	mustInsertBlob(t, s, sampleBlob(hashA, 10))
	err := s.InsertBlob(context.Background(), sampleBlob(hashA, 10))
	requireAlreadyExists(t, err)
}

func conformUnknownBlob(t *testing.T, s store.Store) {
	_, err := s.BlobByHash(context.Background(), hashA)
	requireNotFound(t, err)
}

func conformSetBlobStoredHash(t *testing.T, s store.Store) {
	ctx := context.Background()
	mustInsertBlob(t, s, sampleBlob(hashA, 900))

	if err := s.SetBlobStoredHash(ctx, hashA, hashB); err != nil {
		t.Fatalf("set stored hash: %v", err)
	}
	got, err := s.BlobByHash(ctx, hashA)
	if err != nil {
		t.Fatalf("blob by hash: %v", err)
	}
	if got.Hash != hashA {
		t.Fatalf("blobs.hash was rewritten to %q; D-08 requires it stay %q", got.Hash, hashA)
	}
	if got.StoredHash != hashB {
		t.Fatalf("stored_hash = %q, want %q", got.StoredHash, hashB)
	}
	if got.OriginalReplacedAt == nil || *got.OriginalReplacedAt != testNowMs {
		t.Fatalf("original_replaced_at = %v, want %d", got.OriginalReplacedAt, testNowMs)
	}
	requireNotFound(t, s.SetBlobStoredHash(ctx, hashB, hashA))
}

func conformSetBlobStateAndMedia(t *testing.T, s store.Store) {
	ctx := context.Background()
	mustInsertBlob(t, s, sampleBlob(hashA, 900))

	if err := s.SetBlobState(ctx, hashA, store.BlobMissing); err != nil {
		t.Fatalf("set blob state: %v", err)
	}
	requireNotFound(t, s.SetBlobState(ctx, hashB, store.BlobMissing))

	if err := s.SetBlobMedia(ctx, hashA, 1920, 1080, 30_500); err != nil {
		t.Fatalf("set blob media: %v", err)
	}
	got, err := s.BlobByHash(ctx, hashA)
	if err != nil {
		t.Fatalf("blob by hash: %v", err)
	}
	if got.Width != 1920 || got.Height != 1080 || got.DurationMs != 30_500 {
		t.Fatalf("media = %dx%d %dms, want 1920x1080 30500ms", got.Width, got.Height, got.DurationMs)
	}
	requireNotFound(t, s.SetBlobMedia(ctx, hashB, 1, 1, 1))
}

func conformBlobVariants(t *testing.T, s store.Store) {
	ctx := context.Background()
	mustInsertBlob(t, s, sampleBlob(hashA, 900))

	if _, err := s.BlobVariant(ctx, hashA, store.VariantThumb256); err == nil ||
		!errors.Is(err, core.ErrNotFound()) {
		t.Fatalf("ungenerated variant: want core.ErrNotFound, got %v", err)
	}

	thumb := store.BlobVariant{
		Hash: hashA, Kind: store.VariantThumb256,
		RelPath:   ".homesink/thumbs/aa/" + hashA + "_256.webp",
		SizeBytes: 4096, Width: 256, Height: 192, CreatedAt: testNowMs,
	}
	if err := s.InsertBlobVariant(ctx, thumb); err != nil {
		t.Fatalf("insert variant: %v", err)
	}
	// Re-generating a variant replaces the row rather than failing.
	thumb.SizeBytes = 5120
	if err := s.InsertBlobVariant(ctx, thumb); err != nil {
		t.Fatalf("re-insert variant: %v", err)
	}
	big := store.BlobVariant{
		Hash: hashA, Kind: store.VariantThumb1024,
		RelPath:   ".homesink/thumbs/aa/" + hashA + "_1024.webp",
		SizeBytes: 40960, Width: 1024, Height: 768, CreatedAt: testNowMs,
	}
	if err := s.InsertBlobVariant(ctx, big); err != nil {
		t.Fatalf("insert variant: %v", err)
	}

	got, err := s.BlobVariant(ctx, hashA, store.VariantThumb256)
	if err != nil {
		t.Fatalf("blob variant: %v", err)
	}
	if !reflect.DeepEqual(*got, thumb) {
		t.Fatalf("variant\n got %+v\nwant %+v", *got, thumb)
	}

	list, err := s.BlobVariantsByHash(ctx, hashA)
	if err != nil {
		t.Fatalf("variants by hash: %v", err)
	}
	if want := []store.BlobVariant{big, thumb}; !reflect.DeepEqual(list, want) {
		t.Fatalf("variant list\n got %+v\nwant %+v", list, want)
	}
}

func conformItemRoundTrip(t *testing.T, s store.Store) {
	ctx := context.Background()
	if err := s.InsertDevice(ctx, sampleDevice("dev_1", "hash_1")); err != nil {
		t.Fatalf("insert device: %v", err)
	}
	blob := mustInsertBlob(t, s, sampleBlob(hashA, 2_000_000))

	want := sampleItem("itm_0000000000000001", blob.Hash, "IMG_0001.jpg", 1_735_600_000_000)
	want.DeviceID = "dev_1"
	mustInsertItem(t, s, want)

	byPath, err := s.ItemByPath(ctx, want.RelPath)
	if err != nil {
		t.Fatalf("item by path: %v", err)
	}
	if !reflect.DeepEqual(*byPath, want) {
		t.Fatalf("item by path\n got %+v\nwant %+v", *byPath, want)
	}
	byID, err := s.ItemByID(ctx, want.ItemID)
	if err != nil {
		t.Fatalf("item by id: %v", err)
	}
	if !reflect.DeepEqual(*byID, want) {
		t.Fatalf("item by id\n got %+v\nwant %+v", *byID, want)
	}

	requireNotFound(t, errFrom(s.ItemByPath(ctx, "Camera/2025/12/nope.jpg")))
	requireNotFound(t, errFrom(s.ItemByID(ctx, "itm_nope")))

	stat, err := s.AlbumStat(ctx, "Camera", 2025, 12)
	if err != nil {
		t.Fatalf("album stat: %v", err)
	}
	wantStat := store.AlbumStat{
		Album: "Camera", Year: 2025, Month: 12,
		ItemCount: 1, SizeBytes: blob.SizeBytes, LatestCapturedAtMs: want.CapturedAtMs,
	}
	if !reflect.DeepEqual(*stat, wantStat) {
		t.Fatalf("album stat\n got %+v\nwant %+v", *stat, wantStat)
	}
	requireNotFound(t, errFrom(s.AlbumStat(ctx, "Camera", 2025, 11)))
}

func conformItemWithoutDevice(t *testing.T, s store.Store) {
	ctx := context.Background()
	mustInsertBlob(t, s, sampleBlob(hashA, 10))
	it := mustInsertItem(t, s, sampleItem("itm_1", hashA, "a.jpg", 1_700_000_000_000))

	got, err := s.ItemByID(ctx, it.ItemID)
	if err != nil {
		t.Fatalf("item by id: %v", err)
	}
	if got.DeviceID != "" {
		t.Fatalf("device id = %q, want empty", got.DeviceID)
	}
}

func conformInsertItemUnknownBlob(t *testing.T, s store.Store) {
	err := s.InsertItem(context.Background(), sampleItem("itm_1", hashA, "a.jpg", 1))
	requireNotFound(t, err)
}

func conformInsertItemDuplicatePath(t *testing.T, s store.Store) {
	ctx := context.Background()
	mustInsertBlob(t, s, sampleBlob(hashA, 10))
	mustInsertItem(t, s, sampleItem("itm_1", hashA, "a.jpg", 1))

	dup := sampleItem("itm_2", hashA, "a.jpg", 2)
	requireAlreadyExists(t, s.InsertItem(ctx, dup))
}

func conformItemsByHash(t *testing.T, s store.Store) {
	ctx := context.Background()
	mustInsertBlob(t, s, sampleBlob(hashA, 10))
	first := mustInsertItem(t, s, sampleItem("itm_1", hashA, "a.jpg", 1))
	second := mustInsertItem(t, s, sampleItem("itm_2", hashA, "b.jpg", 2))

	got, err := s.ItemsByHash(ctx, hashA)
	if err != nil {
		t.Fatalf("items by hash: %v", err)
	}
	if want := []core.Item{first, second}; !reflect.DeepEqual(got, want) {
		t.Fatalf("items by hash\n got %+v\nwant %+v", got, want)
	}
	empty, err := s.ItemsByHash(ctx, hashB)
	if err != nil {
		t.Fatalf("items by hash: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("items by unknown hash = %+v, want none", empty)
	}
}

func conformDeleteItemStats(t *testing.T, s store.Store) {
	ctx := context.Background()
	mustInsertBlob(t, s, sampleBlob(hashA, 100))
	mustInsertBlob(t, s, sampleBlob(hashB, 300))
	older := mustInsertItem(t, s, sampleItem("itm_1", hashA, "a.jpg", 1_000))
	newer := mustInsertItem(t, s, sampleItem("itm_2", hashB, "b.jpg", 9_000))

	if err := s.DeleteItem(ctx, newer.ItemID); err != nil {
		t.Fatalf("delete item: %v", err)
	}
	stat, err := s.AlbumStat(ctx, "Camera", 2025, 12)
	if err != nil {
		t.Fatalf("album stat: %v", err)
	}
	want := store.AlbumStat{
		Album: "Camera", Year: 2025, Month: 12,
		ItemCount: 1, SizeBytes: 100, LatestCapturedAtMs: older.CapturedAtMs,
	}
	if !reflect.DeepEqual(*stat, want) {
		t.Fatalf("stat after deleting the newest item\n got %+v\nwant %+v", *stat, want)
	}
	requireNotFound(t, s.DeleteItem(ctx, "itm_gone"))
}

func conformDeleteLastItem(t *testing.T, s store.Store) {
	ctx := context.Background()
	mustInsertBlob(t, s, sampleBlob(hashA, 100))
	only := mustInsertItem(t, s, sampleItem("itm_1", hashA, "a.jpg", 1_000))

	if err := s.DeleteItem(ctx, only.ItemID); err != nil {
		t.Fatalf("delete item: %v", err)
	}
	requireNotFound(t, errFrom(s.AlbumStat(ctx, "Camera", 2025, 12)))
	stats, err := s.AlbumStats(ctx)
	if err != nil {
		t.Fatalf("album stats: %v", err)
	}
	if len(stats) != 0 {
		t.Fatalf("album stats = %+v, want none", stats)
	}
}

func conformAlbumStatsEqualDerived(t *testing.T, s store.Store) {
	ctx := context.Background()
	mustInsertBlob(t, s, sampleBlob(hashA, 100))
	mustInsertBlob(t, s, sampleBlob(hashB, 250))
	mustInsertItem(t, s, sampleItem("itm_1", hashA, "a.jpg", 5_000))
	mustInsertItem(t, s, sampleItem("itm_2", hashB, "b.jpg", 7_000))

	screenshot := core.Item{
		ItemID: "itm_3", Hash: hashA, Album: "Screenshots", Year: 2026, Month: 1,
		Filename: "s.png", RelPath: "Screenshots/2026/01/s.png",
		CapturedAtMs: 8_000, CapturedOffsetMin: 0, CreatedAt: testNowMs,
	}
	mustInsertItem(t, s, screenshot)

	stored, err := s.AlbumStats(ctx)
	if err != nil {
		t.Fatalf("album stats: %v", err)
	}
	derived, err := s.DeriveAlbumStats(ctx)
	if err != nil {
		t.Fatalf("derive album stats: %v", err)
	}
	if !reflect.DeepEqual(stored, derived) {
		t.Fatalf("album_stats drifted from the aggregate\n stored %+v\nderived %+v", stored, derived)
	}
}

func conformJobPriority(t *testing.T, s store.Store) {
	ctx := context.Background()
	transcodeID, err := s.EnqueueJob(ctx, store.NewJob{Kind: store.JobTranscode, Payload: `{"hash":"a"}`})
	if err != nil {
		t.Fatalf("enqueue transcode: %v", err)
	}
	thumbID, err := s.EnqueueJob(ctx, store.NewJob{Kind: store.JobThumbnail, Payload: `{"hash":"a"}`})
	if err != nil {
		t.Fatalf("enqueue thumbnail: %v", err)
	}

	// D-32: thumbnails outrank transcodes, so the later-queued thumb goes first.
	first, err := s.ClaimJob(ctx)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if first.JobID != thumbID {
		t.Fatalf("claimed job %d, want the thumbnail %d", first.JobID, thumbID)
	}
	if first.Priority != store.PriorityThumbnail {
		t.Fatalf("thumbnail priority = %d, want %d", first.Priority, store.PriorityThumbnail)
	}
	if first.State != store.JobRunning || first.Attempts != 1 {
		t.Fatalf("claimed job state=%s attempts=%d, want running/1", first.State, first.Attempts)
	}

	second, err := s.ClaimJob(ctx)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if second.JobID != transcodeID {
		t.Fatalf("claimed job %d, want the transcode %d", second.JobID, transcodeID)
	}
	if second.Priority != store.PriorityTranscode {
		t.Fatalf("transcode priority = %d, want %d", second.Priority, store.PriorityTranscode)
	}
}

func conformClaimNothingDue(t *testing.T, s store.Store) {
	ctx := context.Background()
	if _, err := s.ClaimJob(ctx); !errors.Is(err, core.ErrNotFound()) {
		t.Fatalf("empty queue: want core.ErrNotFound, got %v", err)
	}
	if _, err := s.EnqueueJob(ctx, store.NewJob{
		Kind: store.JobProbe, Payload: "{}", NextAttemptAtMs: testNowMs + 60_000,
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := s.ClaimJob(ctx); !errors.Is(err, core.ErrNotFound()) {
		t.Fatalf("future job: want core.ErrNotFound, got %v", err)
	}
}

func conformFailJob(t *testing.T, s store.Store) {
	ctx := context.Background()
	id, err := s.EnqueueJob(ctx, store.NewJob{Kind: store.JobThumbnail, Payload: "{}"})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	const maxAttempts = 3
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		claimed, err := s.ClaimJob(ctx)
		if err != nil {
			t.Fatalf("claim on attempt %d: %v", attempt, err)
		}
		if claimed.Attempts != attempt {
			t.Fatalf("attempts = %d on attempt %d", claimed.Attempts, attempt)
		}
		if err := s.FailJob(ctx, id, "ffmpeg exit 1", testNowMs, maxAttempts); err != nil {
			t.Fatalf("fail job: %v", err)
		}

		job, err := s.JobByID(ctx, id)
		if err != nil {
			t.Fatalf("job by id: %v", err)
		}
		wantState := store.JobQueued
		if attempt == maxAttempts {
			wantState = store.JobFailed
		}
		if job.State != wantState {
			t.Fatalf("after attempt %d state = %s, want %s", attempt, job.State, wantState)
		}
		if job.LastError != "ffmpeg exit 1" {
			t.Fatalf("last_error = %q", job.LastError)
		}
	}

	if _, err := s.ClaimJob(ctx); !errors.Is(err, core.ErrNotFound()) {
		t.Fatalf("a failed job must not be claimable again, got %v", err)
	}
	requireNotFound(t, s.FailJob(ctx, 9999, "x", 0, maxAttempts))
}

func conformCompleteJob(t *testing.T, s store.Store) {
	ctx := context.Background()
	id, err := s.EnqueueJob(ctx, store.NewJob{Kind: store.JobProbe, Payload: "{}"})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := s.ClaimJob(ctx); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := s.FailJob(ctx, id, "transient", testNowMs, 5); err != nil {
		t.Fatalf("fail job: %v", err)
	}
	if _, err := s.ClaimJob(ctx); err != nil {
		t.Fatalf("re-claim: %v", err)
	}
	if err := s.CompleteJob(ctx, id); err != nil {
		t.Fatalf("complete job: %v", err)
	}

	job, err := s.JobByID(ctx, id)
	if err != nil {
		t.Fatalf("job by id: %v", err)
	}
	if job.State != store.JobDone {
		t.Fatalf("state = %s, want done", job.State)
	}
	if job.LastError != "" {
		t.Fatalf("last_error = %q, want cleared", job.LastError)
	}
	requireNotFound(t, s.CompleteJob(ctx, 9999))
	requireNotFound(t, errFrom(s.JobByID(ctx, 9999)))
}

func conformRequeueRunningJobs(t *testing.T, s store.Store) {
	ctx := context.Background()
	for range 2 {
		if _, err := s.EnqueueJob(ctx, store.NewJob{Kind: store.JobThumbnail, Payload: "{}"}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}
	claimed, err := s.ClaimJob(ctx)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}

	moved, err := s.RequeueRunningJobs(ctx)
	if err != nil {
		t.Fatalf("requeue: %v", err)
	}
	if moved != 1 {
		t.Fatalf("requeued %d jobs, want 1", moved)
	}
	job, err := s.JobByID(ctx, claimed.JobID)
	if err != nil {
		t.Fatalf("job by id: %v", err)
	}
	if job.State != store.JobQueued {
		t.Fatalf("state = %s, want queued", job.State)
	}
	// The attempt is deliberately kept, so a job that kills the process still
	// runs out of attempts instead of looping forever.
	if job.Attempts != 1 {
		t.Fatalf("attempts = %d, want the crashed attempt to still count", job.Attempts)
	}
}

func conformJobsByState(t *testing.T, s store.Store) {
	ctx := context.Background()
	var ids []int64
	for range 3 {
		id, err := s.EnqueueJob(ctx, store.NewJob{Kind: store.JobThumbnail, Payload: "{}"})
		if err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		ids = append(ids, id)
	}

	queued, err := s.JobsByState(ctx, store.JobQueued, 0)
	if err != nil {
		t.Fatalf("jobs by state: %v", err)
	}
	if len(queued) != 3 {
		t.Fatalf("queued = %d, want 3", len(queued))
	}
	for i, job := range queued {
		if job.JobID != ids[i] {
			t.Fatalf("job %d = %d, want %d (ordered by id)", i, job.JobID, ids[i])
		}
	}

	limited, err := s.JobsByState(ctx, store.JobQueued, 2)
	if err != nil {
		t.Fatalf("jobs by state: %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("limited = %d, want 2", len(limited))
	}
	failed, err := s.JobsByState(ctx, store.JobFailed, 0)
	if err != nil {
		t.Fatalf("jobs by state: %v", err)
	}
	if len(failed) != 0 {
		t.Fatalf("failed = %+v, want none", failed)
	}
}

func conformDeviceRoundTrip(t *testing.T, s store.Store) {
	ctx := context.Background()
	want := sampleDevice("dev_1", "argon2id$fake")
	if err := s.InsertDevice(ctx, want); err != nil {
		t.Fatalf("insert device: %v", err)
	}

	byID, err := s.DeviceByID(ctx, want.DeviceID)
	if err != nil {
		t.Fatalf("device by id: %v", err)
	}
	if !reflect.DeepEqual(*byID, want) {
		t.Fatalf("device by id\n got %+v\nwant %+v", *byID, want)
	}
	byToken, err := s.DeviceByTokenHash(ctx, want.TokenHash)
	if err != nil {
		t.Fatalf("device by token hash: %v", err)
	}
	if !reflect.DeepEqual(*byToken, want) {
		t.Fatalf("device by token hash\n got %+v\nwant %+v", *byToken, want)
	}
	requireNotFound(t, errFrom(s.DeviceByID(ctx, "dev_nope")))
	requireNotFound(t, errFrom(s.DeviceByTokenHash(ctx, "nope")))
}

func conformInsertDeviceTwice(t *testing.T, s store.Store) {
	ctx := context.Background()
	if err := s.InsertDevice(ctx, sampleDevice("dev_1", "h1")); err != nil {
		t.Fatalf("insert device: %v", err)
	}
	requireAlreadyExists(t, s.InsertDevice(ctx, sampleDevice("dev_1", "h2")))
}

func conformRevokedDevice(t *testing.T, s store.Store) {
	ctx := context.Background()
	d := sampleDevice("dev_1", "argon2id$fake")
	if err := s.InsertDevice(ctx, d); err != nil {
		t.Fatalf("insert device: %v", err)
	}
	if err := s.RevokeDevice(ctx, d.DeviceID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	// Revoking twice keeps the first timestamp and is not an error.
	if err := s.RevokeDevice(ctx, d.DeviceID); err != nil {
		t.Fatalf("second revoke: %v", err)
	}

	requireNotFound(t, errFrom(s.DeviceByTokenHash(ctx, d.TokenHash)))
	got, err := s.DeviceByID(ctx, d.DeviceID)
	if err != nil {
		t.Fatalf("device by id: %v", err)
	}
	if got.RevokedAt != testNowMs || !got.Revoked() {
		t.Fatalf("revoked_at = %d, want %d", got.RevokedAt, testNowMs)
	}
	requireNotFound(t, s.RevokeDevice(ctx, "dev_nope"))
}

func conformTouchDevice(t *testing.T, s store.Store) {
	ctx := context.Background()
	d := sampleDevice("dev_1", "h1")
	d.AppVersionCode = 0
	if err := s.InsertDevice(ctx, d); err != nil {
		t.Fatalf("insert device: %v", err)
	}

	if err := s.TouchDevice(ctx, d.DeviceID, 41); err != nil {
		t.Fatalf("touch: %v", err)
	}
	got, err := s.DeviceByID(ctx, d.DeviceID)
	if err != nil {
		t.Fatalf("device by id: %v", err)
	}
	if got.LastSeenAt != testNowMs {
		t.Fatalf("last_seen_at = %d, want %d", got.LastSeenAt, testNowMs)
	}
	if got.AppVersionCode != 41 {
		t.Fatalf("app_version_code = %d, want 41", got.AppVersionCode)
	}

	// A zero version code means "unknown" and must not erase what we know.
	if err := s.TouchDevice(ctx, d.DeviceID, 0); err != nil {
		t.Fatalf("touch: %v", err)
	}
	got, err = s.DeviceByID(ctx, d.DeviceID)
	if err != nil {
		t.Fatalf("device by id: %v", err)
	}
	if got.AppVersionCode != 41 {
		t.Fatalf("app_version_code = %d, want 41 kept", got.AppVersionCode)
	}
	requireNotFound(t, s.TouchDevice(ctx, "dev_nope", 1))
}

func conformListDevices(t *testing.T, s store.Store) {
	ctx := context.Background()
	older := sampleDevice("dev_1", "h1")
	older.CreatedAt = testNowMs - 1_000
	newer := sampleDevice("dev_2", "h2")
	for _, d := range []store.Device{newer, older} {
		if err := s.InsertDevice(ctx, d); err != nil {
			t.Fatalf("insert device: %v", err)
		}
	}
	if err := s.RevokeDevice(ctx, older.DeviceID); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	list, err := s.ListDevices(ctx)
	if err != nil {
		t.Fatalf("list devices: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("list = %+v, want 2 devices", list)
	}
	if list[0].DeviceID != older.DeviceID || list[1].DeviceID != newer.DeviceID {
		t.Fatalf("order = %s,%s, want oldest first", list[0].DeviceID, list[1].DeviceID)
	}
	if !list[0].Revoked() {
		t.Fatalf("a revoked device must still be listed, so it can be shown as cut off")
	}
}

func conformPairingCodeRoundTrip(t *testing.T, s store.Store) {
	ctx := context.Background()
	want := samplePairingCode()
	if err := s.InsertPairingCode(ctx, want); err != nil {
		t.Fatalf("insert pairing code: %v", err)
	}
	requireAlreadyExists(t, s.InsertPairingCode(ctx, want))

	got, err := s.PairingCodeByHash(ctx, want.CodeHash)
	if err != nil {
		t.Fatalf("pairing code: %v", err)
	}
	if !reflect.DeepEqual(*got, want) {
		t.Fatalf("pairing code\n got %+v\nwant %+v", *got, want)
	}
	if got.Used() {
		t.Fatalf("a fresh code must not read as used")
	}
	requireNotFound(t, errFrom(s.PairingCodeByHash(ctx, "nope")))
}

func conformPairingCodeSingleUse(t *testing.T, s store.Store) {
	ctx := context.Background()
	pc := store.PairingCode{CodeHash: "h", ExpiresAtMs: testNowMs + 600_000}
	if err := s.InsertPairingCode(ctx, pc); err != nil {
		t.Fatalf("insert pairing code: %v", err)
	}

	if err := s.MarkPairingCodeUsed(ctx, pc.CodeHash); err != nil {
		t.Fatalf("mark used: %v", err)
	}
	requireAlreadyExists(t, s.MarkPairingCodeUsed(ctx, pc.CodeHash))

	got, err := s.PairingCodeByHash(ctx, pc.CodeHash)
	if err != nil {
		t.Fatalf("pairing code: %v", err)
	}
	if !got.Used() || got.UsedAtMs != testNowMs {
		t.Fatalf("used_at = %d, want %d", got.UsedAtMs, testNowMs)
	}
	requireNotFound(t, s.MarkPairingCodeUsed(ctx, "nope"))
}

func conformPairingCodeAttempts(t *testing.T, s store.Store) {
	ctx := context.Background()
	pc := store.PairingCode{CodeHash: "h", ExpiresAtMs: testNowMs + 600_000}
	if err := s.InsertPairingCode(ctx, pc); err != nil {
		t.Fatalf("insert pairing code: %v", err)
	}

	for want := 1; want <= 5; want++ {
		got, err := s.IncrementPairingCodeAttempts(ctx, pc.CodeHash)
		if err != nil {
			t.Fatalf("increment: %v", err)
		}
		if got != want {
			t.Fatalf("attempts = %d, want %d", got, want)
		}
	}
	if _, err := s.IncrementPairingCodeAttempts(ctx, "nope"); !errors.Is(err, core.ErrNotFound()) {
		t.Fatalf("unknown code: want core.ErrNotFound, got %v", err)
	}
}

func conformDeletePairingCodes(t *testing.T, s store.Store) {
	ctx := context.Background()
	expired := store.PairingCode{CodeHash: "old", ExpiresAtMs: testNowMs - 1}
	live := store.PairingCode{CodeHash: "new", ExpiresAtMs: testNowMs + 600_000}
	for _, pc := range []store.PairingCode{expired, live} {
		if err := s.InsertPairingCode(ctx, pc); err != nil {
			t.Fatalf("insert pairing code: %v", err)
		}
	}

	removed, err := s.DeletePairingCodesBefore(ctx, testNowMs)
	if err != nil {
		t.Fatalf("delete expired: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed %d codes, want 1", removed)
	}
	requireNotFound(t, errFrom(s.PairingCodeByHash(ctx, expired.CodeHash)))
	if _, err := s.PairingCodeByHash(ctx, live.CodeHash); err != nil {
		t.Fatalf("live code was swept: %v", err)
	}
}

// errFrom drops the value half of a (T, error) pair so it can be asserted on
// directly.
func errFrom[T any](_ T, err error) error { return err }
