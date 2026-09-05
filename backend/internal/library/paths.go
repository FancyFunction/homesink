package library

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// ErrUnsafePath is returned when a resolved path would leave the library root.
// It exists because the album and the filename both originate at a client that
// the server does not trust with path construction (D-10).
var ErrUnsafePath = errors.New("library: resolved path escapes the library root")

const (
	// collisionSep is D-13's double underscore, chosen so the suffix stays
	// readable and does not look like part of the original name.
	collisionSep = "__"
	// collisionHexLen is how much of the hash D-13 puts in the suffix.
	collisionHexLen = 8
)

// resolved is everything BuildRelPath derives from one placement. It exists so
// Place fills core.Item's Album, Year, Month, Filename and RelPath from a
// single computation, which is what makes invariant S2 hold by construction.
type resolved struct {
	album    string
	year     int
	month    int
	filename string
	relPath  string
}

// BuildRelPath returns the library-relative path Album/YYYY/MM/filename for a
// capture, with the album and filename sanitised (D-10) and the year and month
// taken in the capture-local zone (D-11).
//
// The offset is applied before the year and month are read, so a photo taken
// at 23:30 on 31 December lands in that December wherever the server happens
// to sit. The month is zero-padded because 2025/03 has to sort next to
// 2025/12 in a plain file manager.
func BuildRelPath(album string, capturedAtMs int64, offsetMin int, filename string) string {
	return resolve(album, capturedAtMs, offsetMin, filename).relPath
}

func resolve(album string, capturedAtMs int64, offsetMin int, filename string) resolved {
	a := SanitizeAlbum(album)
	y, m := yearMonth(capturedAtMs, offsetMin)
	f := SanitizeFilename(filename)
	return resolved{album: a, year: y, month: m, filename: f, relPath: joinRel(a, y, m, f)}
}

// yearMonth reads the calendar year and month at the capture site, not at the
// server (D-11). The offset is minutes east of UTC at the capture instant, so
// it already accounts for whichever side of a DST switch the photo falls on.
func yearMonth(capturedAtMs int64, offsetMin int) (year, month int) {
	t := time.UnixMilli(capturedAtMs).In(time.FixedZone("", offsetMin*60))
	return t.Year(), int(t.Month())
}

func joinRel(album string, year, month int, filename string) string {
	return fmt.Sprintf("%s/%04d/%02d/%s", album, year, month, filename)
}

// SuffixedFilename applies D-13's collision suffix: the same basename backed
// by different bytes becomes IMG_0001__a1b2c3d4.jpg. It is a pure function of
// the name and the hash, so a retried commit produces the same name instead of
// fanning out into _1, _2, _3.
func SuffixedFilename(filename, hash string) string {
	if len(hash) < collisionHexLen {
		return filename
	}
	stem, ext := splitExt(filename)
	return fitFilename(stem+collisionSep+hash[:collisionHexLen], ext)
}

// safeAbs joins relPath onto root and proves the result is still inside root
// after every preceding string manipulation. root must already be absolute and
// clean. This is the last line of defence rather than the only one: the
// sanitisers strip separators and traversal long before this runs.
func safeAbs(root, relPath string) (string, error) {
	// A library-relative path is relative. An absolute one would be silently
	// re-rooted by filepath.Join into something confined but not what the
	// caller asked for, which is worth an error rather than a surprise.
	if relPath == "" || strings.HasPrefix(relPath, "/") || strings.HasPrefix(relPath, `\`) ||
		filepath.IsAbs(relPath) {
		return "", fmt.Errorf("%w: %q", ErrUnsafePath, relPath)
	}
	abs := filepath.Join(root, filepath.FromSlash(relPath))
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", fmt.Errorf("%w: %q", ErrUnsafePath, relPath)
	}
	if rel == "." || rel == ".." || filepath.IsAbs(rel) ||
		strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %q", ErrUnsafePath, relPath)
	}
	return abs, nil
}
