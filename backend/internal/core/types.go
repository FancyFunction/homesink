// Package core is the frozen domain contract for the Homesink backend.
//
// Every other backend package imports core; core imports nothing from this
// module in return. After WP-B1 this file and errors.go are FROZEN: later work
// packages may read them but must not edit them. A new Store method is a change
// request against WP-B2, which owns the interface's growth.
package core

import "context"

// MediaType is the coarse classification of a blob, mirrored by the
// media_type CHECK constraint in the SQLite schema.
type MediaType string

// The three media classes Homesink accepts (D-14).
const (
	MediaImage MediaType = "image"
	MediaVideo MediaType = "video"
	MediaAudio MediaType = "audio"
)

// Blob is one distinct uploaded byte stream, identified for all time by the
// SHA-256 of what the client uploaded (D-05, D-08). One blob may back many
// items.
type Blob struct {
	Hash       string
	SizeBytes  int64
	MimeType   string
	MediaType  MediaType
	RelPath    string
	StoredHash string
	// OriginalReplacedAt is set (epoch ms) once the transcoder has replaced
	// the original file on disk; nil while the original is untouched (D-08).
	OriginalReplacedAt *int64
	Width              int
	Height             int
	DurationMs         int64
	CreatedAt          int64
}

// Item is one placement of a blob at Album/Year/Month/filename in the
// human-browsable library tree (D-06).
type Item struct {
	ItemID            string
	Hash              string
	Album             string
	Year              int
	Month             int
	Filename          string
	RelPath           string
	CapturedAtMs      int64
	CapturedOffsetMin int
	DeviceID          string
	CreatedAt         int64
}

// Placement is the client-supplied intent for where a committed blob should
// land. The server derives the final path from it (D-10, D-11, D-13).
type Placement struct {
	Album             string
	CapturedAtMs      int64
	CapturedOffsetMin int
	Filename          string
}

// Store is the whole persistence surface of the backend. It is implemented by
// store.SQLite and faked by testutil.MemStore; both must pass the same
// conformance suite (WP-B2). Later work packages append their own methods here
// and the matching fake methods.
type Store interface {
	BlobByHash(ctx context.Context, hash string) (*Blob, error)
	InsertBlob(ctx context.Context, b Blob) error
	InsertItem(ctx context.Context, it Item) error
	ItemByPath(ctx context.Context, relPath string) (*Item, error)
	// …extended by WP-B2; each later WP appends its own methods and its own fake methods.
}
