// Package library owns the mapping from a client's placement intent onto a
// final path inside the human-browsable library tree, plus the single atomic
// filesystem operation that installs a blob's bytes there.
//
// Everything except Place is pure. SanitizeAlbum and BuildRelPath do no I/O,
// which is what makes the D-10 and D-11 rules exhaustively table-testable.
// Place adds exactly one filesystem mutation — a rename(2) of the staged
// upload, or a hardlink of the blob's canonical file when the bytes are
// already in the sink — and re-verifies, after every string manipulation, that
// the result is still inside the library root.
//
// Place performs no database writes. It reads items to apply the D-13
// collision rule and returns the core.Item its caller persists, because the
// blobs row that item needs as a foreign key may only be created once the file
// has landed (invariant S1, 03-DATA-MODEL.md §1.2).
package library

import (
	"strings"
	"unicode"

	"github.com/rivo/uniseg"
	"golang.org/x/text/unicode/norm"
)

const (
	// AlbumFallback is what an album name that sanitises away becomes (D-09).
	AlbumFallback = "Unsortiert"
	// FilenameFallback is what a filename that sanitises away becomes. Two
	// different blobs that both land on it are separated by the D-13 hash
	// suffix, exactly as any other name clash would be.
	FilenameFallback = "datei"
	// MaxAlbumGraphemes is D-10's length cap, counted in grapheme clusters so
	// truncation never splits an umlaut or an emoji sequence in half.
	MaxAlbumGraphemes = 64
	// maxFilenameBytes keeps a component inside the 255-byte POSIX limit with
	// room to spare for the D-13 "__" + 8 hex collision suffix.
	maxFilenameBytes = 245
)

// forbiddenPathRunes are the characters D-10 strips before anything else can
// interpret them: the Windows-illegal set, which also covers the two path
// separators that would otherwise let an album name build a directory tree.
const forbiddenPathRunes = `/\:*?"<>|`

// reservedNames are the Windows device names D-10 defuses with a trailing
// underscore. They matter because the sink drive is very likely shared over
// SMB, where a directory called CON cannot be created at all.
var reservedNames = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true,
	"com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true,
	"lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

// SanitizeAlbum turns a raw Android bucket name into a directory name that is
// safe on every filesystem the sink drive might be read from. It is pure, does
// no I/O, and is idempotent: sanitising an already-sanitised name is a no-op.
//
// The steps are D-10's, in D-10's order: Unicode NFC, strip the forbidden set
// and the C0 controls, collapse whitespace runs, trim leading and trailing
// dots and spaces, suffix a Windows reserved name with "_", truncate to 64
// grapheme clusters, and finally fall back to "Unsortiert" when nothing is
// left. NFC first is what makes the NFD and NFC spellings of the same umlaut
// one album rather than two.
func SanitizeAlbum(raw string) string {
	s := norm.NFC.String(raw)
	s = stripForbidden(s)
	s = collapseWhitespace(s)
	s = trimDotsAndSpaces(s)
	s = suffixReserved(s)
	s = truncateGraphemes(s, MaxAlbumGraphemes)
	// One step beyond D-10's literal order: truncation can expose a trailing
	// space or dot that the earlier trim removed, and a trailing space is the
	// exact SMB hazard that trim exists to prevent, so it runs again.
	s = trimDotsAndSpaces(s)
	if s == "" {
		return AlbumFallback
	}
	return s
}

// SanitizeFilename applies the same D-10 character rules to the file's name
// component. The API caps filename at 255 characters but constrains nothing
// else, and WP-B5 requires that a hostile filename cannot take part in path
// construction, so the raw value is first reduced to its last path segment —
// "../../etc/passwd" becomes "passwd" — before the album rules run over it.
//
// The reserved-name check applies to the stem, because SMB rejects CON.txt for
// the same reason it rejects CON. The extension is preserved across
// truncation: the client and the browse UI both key off it.
func SanitizeFilename(raw string) string {
	s := norm.NFC.String(raw)
	s = lastPathSegment(s)
	s = stripForbidden(s)
	s = collapseWhitespace(s)
	s = trimDotsAndSpaces(s)

	stem, ext := splitExt(s)
	stem = suffixReserved(stem)
	s = fitFilename(stem, ext)
	if s == "" {
		return FilenameFallback
	}
	return s
}

// lastPathSegment keeps only what follows the final separator. Backslashes
// count as separators here so a Windows-shaped path cannot smuggle a segment
// past the check on a Unix host.
func lastPathSegment(s string) string {
	if i := strings.LastIndexAny(s, `/\`); i >= 0 {
		return s[i+1:]
	}
	return s
}

// stripForbidden removes the D-10 character set and every C0 control.
func stripForbidden(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || strings.ContainsRune(forbiddenPathRunes, r) {
			return -1
		}
		return r
	}, s)
}

// collapseWhitespace replaces every run of whitespace with a single ASCII
// space, so a name padded with non-breaking or ideographic spaces normalises
// the same way a name padded with ordinary ones does.
func collapseWhitespace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inRun := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			inRun = true
			continue
		}
		if inRun && b.Len() > 0 {
			b.WriteByte(' ')
		}
		inRun = false
		b.WriteRune(r)
	}
	return b.String()
}

// trimDotsAndSpaces removes leading and trailing dots and spaces. Trailing
// ones are the dangerous half: Windows silently drops them, which would make
// two distinct albums collide the moment the drive is opened over SMB.
func trimDotsAndSpaces(s string) string { return strings.Trim(s, ". ") }

// suffixReserved defuses a Windows device name by appending an underscore.
func suffixReserved(s string) string {
	if reservedNames[strings.ToLower(s)] {
		return s + "_"
	}
	return s
}

// splitExt splits a filename into its stem and its final extension, including
// the dot. A name that is all extension ("archive.tar.gz" splits to
// "archive.tar" + ".gz") keeps its stem non-empty; a name with no dot returns
// an empty extension.
func splitExt(s string) (stem, ext string) {
	i := strings.LastIndexByte(s, '.')
	if i <= 0 {
		return s, ""
	}
	return s[:i], s[i:]
}

// fitFilename joins stem and ext back together inside maxFilenameBytes,
// truncating the stem on a grapheme boundary when it has to and re-trimming
// whatever dots and spaces the cut exposed.
func fitFilename(stem, ext string) string {
	if len(ext) >= maxFilenameBytes {
		return truncateGraphemeBytes(ext, maxFilenameBytes)
	}
	if len(stem)+len(ext) > maxFilenameBytes {
		stem = truncateGraphemeBytes(stem, maxFilenameBytes-len(ext))
		stem = trimDotsAndSpaces(stem)
	}
	return stem + ext
}

// truncateGraphemes cuts s to at most max grapheme clusters. Counting
// clusters rather than runes or bytes is what keeps a flag, a skin-toned
// emoji or a decomposed umlaut from being sliced into a replacement character.
func truncateGraphemes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	var (
		count int
		end   int
		state = -1
	)
	rest := s
	for rest != "" {
		if count == max {
			return s[:end]
		}
		_, remaining, _, next := uniseg.FirstGraphemeClusterInString(rest, state)
		end = len(s) - len(remaining)
		rest, state = remaining, next
		count++
	}
	return s
}

// truncateGraphemeBytes cuts s to at most maxBytes bytes, never mid-cluster.
func truncateGraphemeBytes(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	var (
		end   int
		state = -1
	)
	rest := s
	for rest != "" {
		_, remaining, _, next := uniseg.FirstGraphemeClusterInString(rest, state)
		next2 := len(s) - len(remaining)
		if next2 > maxBytes {
			break
		}
		end = next2
		rest, state = remaining, next
	}
	return s[:end]
}
