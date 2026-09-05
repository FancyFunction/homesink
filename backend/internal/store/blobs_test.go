package store_test

import (
	"context"
	"testing"

	"github.com/FancyFunction/homesink/backend/internal/core"
	"github.com/FancyFunction/homesink/backend/internal/store"
)

// TestInsertBlobRejectsAnUnknownMediaType: the media_type CHECK is the schema's
// half of the D-14 allowlist, and it must not be bypassable through the store.
func TestInsertBlobRejectsAnUnknownMediaType(t *testing.T) {
	s := newSQLiteStore(t)

	b := sampleBlob(hashA, 10)
	b.MediaType = core.MediaType("document")
	if err := s.InsertBlob(context.Background(), b); err == nil {
		t.Fatal("inserting a non-media blob succeeded, want the media_type CHECK to reject it")
	}
}

// TestInsertBlobStartsStored: a blob row only exists once the file is verified
// and renamed into library/ (invariant S1), so 'stored' is the only correct
// starting state and callers do not get to choose it.
func TestInsertBlobStartsStored(t *testing.T) {
	s := newSQLiteStore(t)
	mustInsertBlob(t, s, sampleBlob(hashA, 10))

	got := rawScalar(t, s.Path(), `SELECT state FROM blobs WHERE hash = ?`, hashA)
	if want := "stored"; got != want {
		t.Fatalf("blobs.state = %v, want %q", got, want)
	}
}

// TestSetBlobStateMarksAMissingFile is the fsck path of invariant S6: a blob
// whose file has vanished is flagged, not deleted, so a dedupe hit can never be
// served from it (D-07).
func TestSetBlobStateMarksAMissingFile(t *testing.T) {
	ctx := context.Background()
	s := newSQLiteStore(t)
	mustInsertBlob(t, s, sampleBlob(hashA, 10))

	if err := s.SetBlobState(ctx, hashA, store.BlobMissing); err != nil {
		t.Fatalf("set blob state: %v", err)
	}
	got := rawScalar(t, s.Path(), `SELECT state FROM blobs WHERE hash = ?`, hashA)
	if want := "missing"; got != want {
		t.Fatalf("blobs.state = %v, want %q", got, want)
	}
}

// TestBlobOptionalColumnsAreNullNotZero: width/height/duration and stored_hash
// are genuinely unknown for a freshly committed blob, and a stored 0 would be a
// claim about the file rather than an absence of one.
func TestBlobOptionalColumnsAreNullNotZero(t *testing.T) {
	s := newSQLiteStore(t)

	b := core.Blob{
		Hash: hashA, SizeBytes: 10, MimeType: "audio/mp4", MediaType: core.MediaAudio,
		RelPath: "Unsortiert/2025/01/a.m4a", CreatedAt: testNowMs,
	}
	mustInsertBlob(t, s, b)

	for _, column := range []string{"stored_hash", "original_replaced_at", "width", "height", "duration_ms"} {
		got := rawScalar(t, s.Path(),
			`SELECT `+column+` IS NULL FROM blobs WHERE hash = ?`, hashA)
		if got != int64(1) {
			t.Errorf("blobs.%s is not NULL", column)
		}
	}
}

// TestInsertBlobVariantRejectsAnUnknownKind guards the blob_variants CHECK, which
// keeps the thumbnail cache and the transcode from inventing new kinds.
func TestInsertBlobVariantRejectsAnUnknownKind(t *testing.T) {
	ctx := context.Background()
	s := newSQLiteStore(t)
	mustInsertBlob(t, s, sampleBlob(hashA, 10))

	v := store.BlobVariant{
		Hash: hashA, Kind: store.VariantKind("thumb512"),
		RelPath: "x.webp", SizeBytes: 1, CreatedAt: testNowMs,
	}
	if err := s.InsertBlobVariant(ctx, v); err == nil {
		t.Fatal("inserting an unknown variant kind succeeded, want the CHECK to reject it")
	}
}

// TestInsertBlobVariantRequiresAnExistingBlob: a variant is derived from content,
// so a row with no blob behind it could never point at a real file (invariant S5).
func TestInsertBlobVariantRequiresAnExistingBlob(t *testing.T) {
	v := store.BlobVariant{
		Hash: hashA, Kind: store.VariantThumb256,
		RelPath: "x.webp", SizeBytes: 1, CreatedAt: testNowMs,
	}
	if err := newSQLiteStore(t).InsertBlobVariant(context.Background(), v); err == nil {
		t.Fatal("inserting a variant for an unknown blob succeeded, want the foreign key to reject it")
	}
}
