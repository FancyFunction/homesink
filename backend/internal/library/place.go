package library

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/FancyFunction/homesink/backend/internal/core"
)

var (
	// ErrInvalidHash rejects anything that is not a lowercase hex SHA-256.
	// The hash names a file on disk, so it is validated before it is used to
	// build one.
	ErrInvalidHash = errors.New("library: blob hash is not 64 lowercase hex characters")
	// ErrNoSource is returned when neither the staged upload nor a stored
	// canonical file exists for the blob, so there are no bytes to place.
	ErrNoSource = errors.New("library: no staged upload and no stored file for blob")
	// ErrCollision is returned when even the D-13 hash-suffixed name is taken
	// by different bytes. Reaching it needs two blobs that share an 8-hex
	// prefix and the same album, month and basename.
	ErrCollision = errors.New("library: hash-suffixed filename is taken by another blob")
)

const (
	// stagedSuffix names the in-flight upload written by WP-B4 as
	// .homesink/staging/<sha256>.part (00-ARCHITECTURE.md §5).
	stagedSuffix = ".part"
	// itemIDPrefix and itemIDHexLen are the schema's "itm_" + 16 hex
	// identifier shape (03-DATA-MODEL.md §1.1).
	itemIDPrefix = "itm_"
	itemIDHexLen = 16
	// dirPerm and linkTmpPrefix cover the two things Place creates: the
	// Album/YYYY/MM directories, and the short-lived hardlink it renames from.
	dirPerm       = 0o755
	linkTmpPrefix = ".link-"
)

// Placer resolves a core.Placement to a final library path and installs the
// blob's bytes there. It holds no state beyond its roots, so one Placer is
// shared by every request; the per-rel_path serialisation that concurrent
// commits need is WP-B4's keyed mutex (01-DECISIONS.md §12).
type Placer struct {
	store   core.Store
	root    string
	staging string

	// Now returns epoch milliseconds for Item.CreatedAt; nil means time.Now.
	Now func() int64
	// NewItemID mints an item id; nil means "itm_" + 16 random hex.
	NewItemID func() (string, error)
}

// NewPlacer returns a Placer over the library root and the staging directory,
// normally $HOMESINK_DATA/library and $HOMESINK_DATA/.homesink/staging. Both
// must sit on one filesystem, which is what makes the install a rename(2)
// rather than a copy (00-ARCHITECTURE.md §5).
func NewPlacer(st core.Store, libraryRoot, stagingDir string) (*Placer, error) {
	if st == nil {
		return nil, errors.New("library: store is required")
	}
	root, err := filepath.Abs(libraryRoot)
	if err != nil {
		return nil, fmt.Errorf("library: library root %q: %w", libraryRoot, err)
	}
	staging, err := filepath.Abs(stagingDir)
	if err != nil {
		return nil, fmt.Errorf("library: staging dir %q: %w", stagingDir, err)
	}
	return &Placer{store: st, root: filepath.Clean(root), staging: filepath.Clean(staging)}, nil
}

// Root is the absolute library root the Placer will never write outside of.
func (p *Placer) Root() string { return p.root }

// Place turns pl into a final path for blobHash, installs the bytes there with
// one atomic filesystem operation, and returns the core.Item its caller
// persists. It writes nothing to the database: the item's blobs row is created
// by the caller after the file has landed (invariant S1).
//
// Collisions follow D-13. Same directory, same basename and the same hash is
// the same file: no write happens and the placement already recorded is
// returned unchanged, which is how a retried commit stays idempotent. A
// different hash earns the deterministic __<8hex> suffix, so retrying that
// produces the same name rather than a new one each time.
//
// Item.DeviceID is left empty. The device that uploaded is the caller's
// knowledge, not the placement's.
func (p *Placer) Place(ctx context.Context, blobHash string, pl core.Placement) (core.Item, error) {
	if !validHash(blobHash) {
		return core.Item{}, fmt.Errorf("%w: %d characters", ErrInvalidHash, len(blobHash))
	}

	r, existing, err := p.resolveCollision(ctx, blobHash, pl)
	if err != nil {
		return core.Item{}, err
	}
	if existing != nil {
		return *existing, nil
	}

	abs, err := safeAbs(p.root, r.relPath)
	if err != nil {
		return core.Item{}, err
	}
	if err := p.install(ctx, blobHash, abs); err != nil {
		return core.Item{}, err
	}

	id, err := p.newItemID()
	if err != nil {
		return core.Item{}, err
	}
	return core.Item{
		ItemID:            id,
		Hash:              blobHash,
		Album:             r.album,
		Year:              r.year,
		Month:             r.month,
		Filename:          r.filename,
		RelPath:           r.relPath,
		CapturedAtMs:      pl.CapturedAtMs,
		CapturedOffsetMin: pl.CapturedOffsetMin,
		CreatedAt:         p.now(),
	}, nil
}

// resolveCollision applies D-13. It returns either the placement to create, or
// a non-nil existing item meaning the file is already placed and nothing at
// all should be written.
func (p *Placer) resolveCollision(ctx context.Context, blobHash string, pl core.Placement) (
	resolved, *core.Item, error) {
	r := resolve(pl.Album, pl.CapturedAtMs, pl.CapturedOffsetMin, pl.Filename)

	occupant, err := p.itemAt(ctx, r.relPath)
	if err != nil {
		return resolved{}, nil, err
	}
	if occupant == nil {
		return r, nil, nil
	}
	if occupant.Hash == blobHash {
		return r, occupant, nil
	}

	r.filename = SuffixedFilename(r.filename, blobHash)
	r.relPath = joinRel(r.album, r.year, r.month, r.filename)

	occupant, err = p.itemAt(ctx, r.relPath)
	switch {
	case err != nil:
		return resolved{}, nil, err
	case occupant == nil:
		return r, nil, nil
	case occupant.Hash == blobHash:
		return r, occupant, nil
	default:
		return resolved{}, nil, fmt.Errorf("%w: %q", ErrCollision, r.relPath)
	}
}

// itemAt reports the placement at relPath, or nil when the path is free.
func (p *Placer) itemAt(ctx context.Context, relPath string) (*core.Item, error) {
	it, err := p.store.ItemByPath(ctx, relPath)
	if err == nil {
		return it, nil
	}
	if errors.Is(err, core.ErrNotFound()) {
		return nil, nil
	}
	return nil, fmt.Errorf("library: look up %q: %w", relPath, err)
}

// install puts the blob's bytes at absPath with one atomic operation.
//
// The ordinary path is a commit: WP-B4 has already staged and fsync'd
// .homesink/staging/<hash>.part, so a rename(2) within the one filesystem
// publishes it — nothing partial is ever visible under library/ (principle 4).
//
// With no staged file this is a second placement of a blob already in the sink
// — the same photo in another album, or from another phone (D-06, D-07). The
// canonical file is hardlinked instead, so the second album really does
// contain the photo when someone plugs the drive into a laptop (principle 5)
// and it costs no extra bytes. The link is made under a temporary name and
// renamed into place, so this path is atomic too.
func (p *Placer) install(ctx context.Context, blobHash, absPath string) error {
	if err := os.MkdirAll(filepath.Dir(absPath), dirPerm); err != nil {
		return fmt.Errorf("library: create album directory: %w", err)
	}

	staged := filepath.Join(p.staging, blobHash+stagedSuffix)
	switch _, err := os.Stat(staged); {
	case err == nil:
		if err := os.Rename(staged, absPath); err != nil {
			return fmt.Errorf("library: publish staged upload: %w", err)
		}
		return syncDir(filepath.Dir(absPath))
	case !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("library: stat staged upload: %w", err)
	}

	return p.linkCanonical(ctx, blobHash, absPath)
}

func (p *Placer) linkCanonical(ctx context.Context, blobHash, absPath string) error {
	blob, err := p.store.BlobByHash(ctx, blobHash)
	if err != nil {
		if errors.Is(err, core.ErrNotFound()) {
			return fmt.Errorf("%w: %s", ErrNoSource, blobHash)
		}
		return fmt.Errorf("library: look up blob: %w", err)
	}
	src, err := safeAbs(p.root, blob.RelPath)
	if err != nil {
		return err
	}
	srcInfo, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrNoSource, blobHash, err)
	}
	if dstInfo, err := os.Stat(absPath); err == nil && os.SameFile(srcInfo, dstInfo) {
		return nil
	}

	tmp, err := p.tempLinkPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(p.staging, dirPerm); err != nil {
		return fmt.Errorf("library: create staging directory: %w", err)
	}
	if err := os.Link(src, tmp); err != nil {
		return fmt.Errorf("library: hardlink canonical file: %w", err)
	}
	if err := os.Rename(tmp, absPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("library: publish hardlink: %w", err)
	}
	return syncDir(filepath.Dir(absPath))
}

func (p *Placer) tempLinkPath() (string, error) {
	suffix, err := randomHex(itemIDHexLen)
	if err != nil {
		return "", err
	}
	return filepath.Join(p.staging, linkTmpPrefix+suffix), nil
}

// syncDir fsyncs a directory so the rename that just published a file survives
// a power cut, not just a process crash (principle 4). A filesystem that
// refuses to open a directory read-only is not an error worth failing a
// placement over.
func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return nil //nolint:nilerr // best-effort durability, the rename already happened
	}
	defer func() { _ = f.Close() }()
	_ = f.Sync()
	return nil
}

func (p *Placer) now() int64 {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now().UnixMilli()
}

func (p *Placer) newItemID() (string, error) {
	if p.NewItemID != nil {
		return p.NewItemID()
	}
	h, err := randomHex(itemIDHexLen)
	if err != nil {
		return "", err
	}
	return itemIDPrefix + h, nil
}

func randomHex(n int) (string, error) {
	b := make([]byte, n/2)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("library: read random bytes: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// validHash accepts only the lowercase hex SHA-256 the wire contract promises
// (openapi.yaml: ^[a-f0-9]{64}$). It is a path component, so nothing looser is
// safe.
func validHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
