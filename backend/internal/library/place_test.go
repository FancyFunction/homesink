package library

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FancyFunction/homesink/backend/internal/core"
	"github.com/FancyFunction/homesink/backend/internal/testutil"
)

const (
	hashA = "a1a2a3a4b5b6b7b8c9cacbcccdcecfd0e1e2e3e4f5f6f7f8090a0b0c0d0e0f00"
	hashB = "b1b2b3b4c5c6c7c8d9dadbdcedeeef00112233445566778899aabbccddeeff00"
)

type fixture struct {
	t       *testing.T
	dir     string
	root    string
	staging string
	store   *testutil.MemStore
	placer  *Placer
	ids     int
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	dir := t.TempDir()
	f := &fixture{
		t:       t,
		dir:     dir,
		root:    filepath.Join(dir, "library"),
		staging: filepath.Join(dir, ".homesink", "staging"),
		store:   testutil.NewMemStore(),
	}
	for _, d := range []string{f.root, f.staging} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("create %s: %v", d, err)
		}
	}

	p, err := NewPlacer(f.store, f.root, f.staging)
	if err != nil {
		t.Fatalf("NewPlacer: %v", err)
	}
	p.Now = func() int64 { return 1_700_000_000_000 }
	p.NewItemID = func() (string, error) {
		f.ids++
		return itemIDPrefix + strings.Repeat("0", 15) + string(rune('0'+f.ids)), nil
	}
	f.placer = p
	return f
}

// stage writes an in-flight upload where WP-B4 would have left it.
func (f *fixture) stage(hash, content string) {
	f.t.Helper()
	path := filepath.Join(f.staging, hash+stagedSuffix)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		f.t.Fatalf("stage %s: %v", hash, err)
	}
}

// persist records what WP-B4's commit transaction would record, so the next
// Place call sees the placement that the previous one produced.
func (f *fixture) persist(it core.Item, size int64) {
	f.t.Helper()
	ctx := context.Background()
	if _, err := f.store.BlobByHash(ctx, it.Hash); errors.Is(err, core.ErrNotFound()) {
		blob := core.Blob{
			Hash: it.Hash, SizeBytes: size, MimeType: "image/jpeg",
			MediaType: core.MediaImage, RelPath: it.RelPath, CreatedAt: 1,
		}
		if err := f.store.InsertBlob(ctx, blob); err != nil {
			f.t.Fatalf("insert blob: %v", err)
		}
	}
	if err := f.store.InsertItem(ctx, it); err != nil {
		f.t.Fatalf("insert item: %v", err)
	}
}

// files lists every regular file under the library root, relative to it.
func (f *fixture) files() []string {
	f.t.Helper()
	var out []string
	err := filepath.WalkDir(f.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(f.root, path)
		if rerr != nil {
			return rerr
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		f.t.Fatalf("walk library: %v", err)
	}
	return out
}

func placement(album, filename string) core.Placement {
	return core.Placement{
		Album:             album,
		CapturedAtMs:      newYearsEveMs,
		CapturedOffsetMin: 120,
		Filename:          filename,
	}
}

// TestPlacePublishesStagedUploadAtomically is the ordinary commit path: the
// staged file moves into the library tree with one rename and nothing is left
// behind in staging.
func TestPlacePublishesStagedUploadAtomically(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.stage(hashA, "alpha")

	it, err := f.placer.Place(context.Background(), hashA, placement("Camera", "IMG_0001.jpg"))
	if err != nil {
		t.Fatalf("Place: %v", err)
	}

	if it.RelPath != "Camera/2025/12/IMG_0001.jpg" {
		t.Errorf("RelPath = %q, want %q", it.RelPath, "Camera/2025/12/IMG_0001.jpg")
	}
	if it.Album != "Camera" || it.Year != 2025 || it.Month != 12 || it.Filename != "IMG_0001.jpg" {
		t.Errorf("item components disagree with RelPath: %+v", it)
	}
	if it.Hash != hashA || it.CapturedAtMs != newYearsEveMs || it.CapturedOffsetMin != 120 {
		t.Errorf("item did not carry the placement through: %+v", it)
	}
	if !strings.HasPrefix(it.ItemID, itemIDPrefix) {
		t.Errorf("ItemID = %q, want the %q prefix", it.ItemID, itemIDPrefix)
	}

	got, err := os.ReadFile(filepath.Join(f.root, "Camera", "2025", "12", "IMG_0001.jpg"))
	if err != nil {
		t.Fatalf("read placed file: %v", err)
	}
	if string(got) != "alpha" {
		t.Errorf("placed file contains %q, want %q", got, "alpha")
	}
	if _, err := os.Stat(filepath.Join(f.staging, hashA+stagedSuffix)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("staged file survived the move: %v", err)
	}
}

// TestPlaceSameNameSameHashCreatesNoNewFile is D-13's first branch and the
// WP-B5 acceptance criterion "same name + same hash → no new file": the
// placement already recorded comes back unchanged and the tree is untouched.
func TestPlaceSameNameSameHashCreatesNoNewFile(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newFixture(t)
	f.stage(hashA, "alpha")

	first, err := f.placer.Place(ctx, hashA, placement("Camera", "IMG_0001.jpg"))
	if err != nil {
		t.Fatalf("first Place: %v", err)
	}
	f.persist(first, 5)
	before := f.files()

	second, err := f.placer.Place(ctx, hashA, placement("Camera", "IMG_0001.jpg"))
	if err != nil {
		t.Fatalf("second Place: %v", err)
	}

	if second.ItemID != first.ItemID || second.RelPath != first.RelPath {
		t.Errorf("second Place invented a placement: %+v, want %+v", second, first)
	}
	after := f.files()
	if len(after) != len(before) || len(after) != 1 {
		t.Errorf("library holds %v, want exactly the one file %v", after, before)
	}
}

// TestPlaceSameNameDifferentHashGetsHashSuffix is D-13's second branch and the
// WP-B5 acceptance criterion "same name + different hash → __<8hex> suffix":
// IMG_0001.jpg from two phones must not overwrite one another.
func TestPlaceSameNameDifferentHashGetsHashSuffix(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newFixture(t)

	f.stage(hashA, "alpha")
	first, err := f.placer.Place(ctx, hashA, placement("Camera", "IMG_0001.jpg"))
	if err != nil {
		t.Fatalf("first Place: %v", err)
	}
	f.persist(first, 5)

	f.stage(hashB, "bravo")
	second, err := f.placer.Place(ctx, hashB, placement("Camera", "IMG_0001.jpg"))
	if err != nil {
		t.Fatalf("second Place: %v", err)
	}
	f.persist(second, 5)

	wantName := "IMG_0001" + collisionSep + hashB[:collisionHexLen] + ".jpg"
	if second.Filename != wantName {
		t.Errorf("Filename = %q, want %q", second.Filename, wantName)
	}
	if second.RelPath != "Camera/2025/12/"+wantName {
		t.Errorf("RelPath = %q, want %q", second.RelPath, "Camera/2025/12/"+wantName)
	}

	original, err := os.ReadFile(filepath.Join(f.root, "Camera", "2025", "12", "IMG_0001.jpg"))
	if err != nil || string(original) != "alpha" {
		t.Errorf("the first file was disturbed: %q, %v", original, err)
	}
	suffixed, err := os.ReadFile(filepath.Join(f.root, "Camera", "2025", "12", wantName))
	if err != nil || string(suffixed) != "bravo" {
		t.Errorf("the suffixed file is wrong: %q, %v", suffixed, err)
	}
}

// TestPlaceRepeatedYieldsTheSameName is the WP-B5 acceptance criterion
// "repeating the operation yields the same name". A retried commit — the normal
// case on a phone whose WiFi dropped — must land on the placement it already
// made, not fan out into _1, _2, _3.
func TestPlaceRepeatedYieldsTheSameName(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newFixture(t)

	f.stage(hashA, "alpha")
	first, err := f.placer.Place(ctx, hashA, placement("Camera", "IMG_0001.jpg"))
	if err != nil {
		t.Fatalf("place hashA: %v", err)
	}
	f.persist(first, 5)

	f.stage(hashB, "bravo")
	collided, err := f.placer.Place(ctx, hashB, placement("Camera", "IMG_0001.jpg"))
	if err != nil {
		t.Fatalf("place hashB: %v", err)
	}
	f.persist(collided, 5)
	before := f.files()

	for attempt := range 3 {
		again, err := f.placer.Place(ctx, hashB, placement("Camera", "IMG_0001.jpg"))
		if err != nil {
			t.Fatalf("retry %d: %v", attempt, err)
		}
		if again.RelPath != collided.RelPath {
			t.Fatalf("retry %d produced %q, want %q", attempt, again.RelPath, collided.RelPath)
		}
		if again.ItemID != collided.ItemID {
			t.Fatalf("retry %d minted a new item %q, want %q", attempt, again.ItemID, collided.ItemID)
		}
	}
	if after := f.files(); len(after) != len(before) {
		t.Errorf("retries created files: %v, want %v", after, before)
	}
}

// TestPlaceCannotEscapeLibraryRoot is the WP-B5 acceptance criterion "escaping
// the root is impossible for any input". Every hostile album and filename here
// must either be neutralised into a path under the root or refused outright,
// and nothing may ever be written outside it.
func TestPlaceCannotEscapeLibraryRoot(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	hostile := []struct{ album, filename string }{
		{"../../etc", "passwd"},
		{"..", ".."},
		{"/etc", "/etc/passwd"},
		{`..\..\windows`, `..\..\windows\system32\cmd.exe`},
		{"Camera/../../..", "../../../../../../etc/shadow"},
		{"", ""},
		{".", "."},
		{"...", "..."},
		{"\x00\x01\x02", "\x00\x01\x02"},
		{strings.Repeat("../", 100), strings.Repeat("../", 100) + "evil.jpg"},
		{"C:\\Windows", "C:\\Windows\\evil.exe"},
		{"CON", "CON"},
		{"\U0001F4F7", "\U0001F4F7.jpg"},
	}

	for i, tc := range hostile {
		f := newFixture(t)
		// A canary the placement must never be able to reach or replace.
		canary := filepath.Join(f.dir, "outside.txt")
		if err := os.WriteFile(canary, []byte("untouched"), 0o644); err != nil {
			t.Fatalf("write canary: %v", err)
		}
		f.stage(hashA, "alpha")

		it, err := f.placer.Place(ctx, hashA, placement(tc.album, tc.filename))
		if err != nil {
			if !errors.Is(err, ErrUnsafePath) {
				t.Errorf("case %d (%q, %q): refused with %v, want ErrUnsafePath", i, tc.album, tc.filename, err)
			}
			continue
		}

		if strings.Contains(it.RelPath, "..") || strings.HasPrefix(it.RelPath, "/") {
			t.Errorf("case %d: RelPath = %q, which is not confined", i, it.RelPath)
		}
		abs := filepath.Join(f.root, filepath.FromSlash(it.RelPath))
		rel, rerr := filepath.Rel(f.root, abs)
		if rerr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			t.Errorf("case %d: %q resolves outside the root", i, it.RelPath)
		}
		if _, serr := os.Stat(abs); serr != nil {
			t.Errorf("case %d: no file at the placed path %q: %v", i, abs, serr)
		}
		if got, rerr := os.ReadFile(canary); rerr != nil || string(got) != "untouched" {
			t.Errorf("case %d: the canary outside the root was disturbed: %q, %v", i, got, rerr)
		}
		for _, name := range f.files() {
			if strings.Contains(name, "..") {
				t.Errorf("case %d: wrote %q inside the library", i, name)
			}
		}
	}
}

// TestPlaceHardlinksWhenTheBlobIsAlreadyStored covers the second placement of
// one blob (D-06, D-07): the same photo in another album, with no staged bytes
// to move. The album really contains the photo, and it costs no extra bytes.
func TestPlaceHardlinksWhenTheBlobIsAlreadyStored(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newFixture(t)
	f.stage(hashA, "alpha")

	first, err := f.placer.Place(ctx, hashA, placement("Camera", "IMG_0001.jpg"))
	if err != nil {
		t.Fatalf("first Place: %v", err)
	}
	f.persist(first, 5)

	second, err := f.placer.Place(ctx, hashA, placement("Urlaub", "IMG_0001.jpg"))
	if err != nil {
		t.Fatalf("second Place: %v", err)
	}
	if second.RelPath != "Urlaub/2025/12/IMG_0001.jpg" {
		t.Fatalf("RelPath = %q, want %q", second.RelPath, "Urlaub/2025/12/IMG_0001.jpg")
	}

	srcInfo, err := os.Stat(filepath.Join(f.root, filepath.FromSlash(first.RelPath)))
	if err != nil {
		t.Fatalf("stat canonical file: %v", err)
	}
	dstInfo, err := os.Stat(filepath.Join(f.root, filepath.FromSlash(second.RelPath)))
	if err != nil {
		t.Fatalf("stat second placement: %v", err)
	}
	if !os.SameFile(srcInfo, dstInfo) {
		t.Error("the second placement is a copy, not a hardlink to the canonical file")
	}
	if leftovers, _ := filepath.Glob(filepath.Join(f.staging, linkTmpPrefix+"*")); len(leftovers) != 0 {
		t.Errorf("temporary hardlinks left in staging: %v", leftovers)
	}
}

// TestPlaceWithoutBytesFails guards the case where a caller asks for a
// placement of a blob that is neither staged nor stored: there is nothing to
// place, and inventing an empty file would break invariant S6.
func TestPlaceWithoutBytesFails(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	_, err := f.placer.Place(context.Background(), hashA, placement("Camera", "IMG_0001.jpg"))
	if !errors.Is(err, ErrNoSource) {
		t.Fatalf("Place = %v, want ErrNoSource", err)
	}
	if files := f.files(); len(files) != 0 {
		t.Errorf("a failed placement left %v behind", files)
	}
}

// TestPlaceRejectsMalformedHash keeps a value that is not a SHA-256 out of the
// staging path it would otherwise be interpolated into.
func TestPlaceRejectsMalformedHash(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	bad := []string{"", "short", strings.ToUpper(hashA), "../../etc/passwd", hashA + "a", strings.Repeat("z", 64)}
	for _, h := range bad {
		if _, err := f.placer.Place(context.Background(), h, placement("Camera", "a.jpg")); !errors.Is(err, ErrInvalidHash) {
			t.Errorf("Place(%q) = %v, want ErrInvalidHash", h, err)
		}
	}
}
