package library

import (
	"strings"
	"testing"

	"golang.org/x/text/unicode/norm"
)

// TestSanitizeAlbum is the D-10 table the WP-B5 acceptance criteria name:
// "../../etc", "CON", "  .hidden  ", a 300-char album, emoji, an empty string,
// "Camera/Sub", and the NFD and NFC spellings of the same umlaut.
func TestSanitizeAlbum(t *testing.T) {
	t.Parallel()

	longAlbum := strings.Repeat("a", 300)

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"parent_traversal", "../../etc", "etc"},
		{"windows_reserved_con", "CON", "CON_"},
		{"windows_reserved_lowercase", "com1", "com1_"},
		{"dot_hidden_padded", "  .hidden  ", "hidden"},
		{"album_300_chars", longAlbum, strings.Repeat("a", MaxAlbumGraphemes)},
		{"emoji", "📷 Urlaub 🏖", "📷 Urlaub 🏖"},
		{"empty", "", AlbumFallback},
		{"whitespace_only", " \u00a0 ", AlbumFallback},
		{"dots_only", "...", AlbumFallback},
		{"camera_sub", "Camera/Sub", "CameraSub"},
		{"nfc_umlaut", "M\u00fcnchen", "M\u00fcnchen"},
		{"nfd_umlaut", "Mu\u0308nchen", "M\u00fcnchen"},
		{"backslash_and_colon", `C:\Fotos`, "CFotos"},
		{"c0_controls", "Ur\x00lau\x1fb", "Urlaub"},
		{"collapses_whitespace_runs", "Foto   Album", "Foto Album"},
		{"collapses_exotic_whitespace", "Foto\u00a0\u3000Album", "Foto Album"},
		{"already_sanitised_is_unchanged", "Screenshots", "Screenshots"},
		{"reserved_with_extension_is_not_a_device", "CON.old", "CON.old"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := SanitizeAlbum(tc.in); got != tc.want {
				t.Errorf("SanitizeAlbum(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestSanitizeAlbumNFDAndNFCCollapseToOneAlbum is the half of the table that a
// per-case expectation cannot state: the two spellings must not merely each be
// correct, they must be the same string, or the same holiday ends up in two
// directories that look identical in a file manager.
func TestSanitizeAlbumNFDAndNFCCollapseToOneAlbum(t *testing.T) {
	t.Parallel()

	const composed = "M\u00fcnchen"    // NFC: u-umlaut as one rune
	const decomposed = "Mu\u0308nchen" // NFD: u + combining diaeresis

	if composed == decomposed {
		t.Fatal("test inputs are not actually different byte sequences")
	}
	nfc, nfd := SanitizeAlbum(composed), SanitizeAlbum(decomposed)
	if nfc != nfd {
		t.Errorf("SanitizeAlbum collapsed to two albums: %q vs %q", nfc, nfd)
	}
	if !norm.NFC.IsNormalString(nfc) {
		t.Errorf("SanitizeAlbum(%q) = %q, which is not NFC", composed, nfc)
	}
}

// TestSanitizeAlbumIsIdempotent guards the property Place relies on when it
// re-derives a path for a retried commit: sanitising twice changes nothing.
func TestSanitizeAlbumIsIdempotent(t *testing.T) {
	t.Parallel()

	inputs := []string{
		"../../etc", "CON", "  .hidden  ", strings.Repeat("a", 300), "📷 Urlaub 🏖",
		"", "Camera/Sub", "Mu\u0308nchen", `C:\Fotos`, "Foto\u00a0\u3000Album",
	}
	for _, in := range inputs {
		once := SanitizeAlbum(in)
		if twice := SanitizeAlbum(once); twice != once {
			t.Errorf("SanitizeAlbum(%q): %q sanitised again to %q", in, once, twice)
		}
	}
}

// TestSanitizeAlbumTruncatesOnGraphemeBoundary checks that the 64-character cap
// counts what a human counts. Truncating a ZWJ emoji sequence by runes or bytes
// would leave a mangled cluster in a directory name.
func TestSanitizeAlbumTruncatesOnGraphemeBoundary(t *testing.T) {
	t.Parallel()

	const family = "\U0001F468\u200D\U0001F469\u200D\U0001F467\u200D\U0001F466" // one cluster
	got := SanitizeAlbum(strings.Repeat(family, 70))

	want := strings.Repeat(family, MaxAlbumGraphemes)
	if got != want {
		t.Errorf("SanitizeAlbum truncated %d clusters to %q", 70, got)
	}
	if strings.HasSuffix(got, "\u200D") {
		t.Error("truncation left a dangling zero-width joiner")
	}
}

// TestSanitizeFilename covers the same rules applied to the name component,
// which WP-B5 requires because a hostile filename must not be able to take part
// in path construction either.
func TestSanitizeFilename(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"ordinary", "IMG_0001.jpg", "IMG_0001.jpg"},
		{"absolute_path", "/etc/passwd", "passwd"},
		{"parent_traversal", "../../../etc/passwd", "passwd"},
		{"windows_path", `..\..\windows\system32\cmd.exe`, "cmd.exe"},
		{"bare_parent", "..", FilenameFallback},
		{"empty", "", FilenameFallback},
		{"dot_leading", ".hidden.jpg", "hidden.jpg"},
		{"reserved_stem", "CON.jpg", "CON_.jpg"},
		{"c0_controls", "IMG\x000001.jpg", "IMG0001.jpg"},
		{"nfd_umlaut", "Gru\u0308\u00dfe.jpg", "Gr\u00fc\u00dfe.jpg"},
		{"trailing_space", "photo .jpg", "photo .jpg"},
		{"trailing_dot", "photo.jpg.", "photo.jpg"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := SanitizeFilename(tc.in); got != tc.want {
				t.Errorf("SanitizeFilename(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestSanitizeFilenameFitsInsideAComponentLimit keeps a very long name, plus the
// D-13 suffix that may be appended to it later, inside the 255-byte POSIX limit
// for one path component.
func TestSanitizeFilenameFitsInsideAComponentLimit(t *testing.T) {
	t.Parallel()

	got := SanitizeFilename(strings.Repeat("\u00e4", 400) + ".jpeg")
	if len(got) > maxFilenameBytes {
		t.Errorf("sanitised filename is %d bytes, want at most %d", len(got), maxFilenameBytes)
	}
	if !strings.HasSuffix(got, ".jpeg") {
		t.Errorf("truncation dropped the extension: %q", got)
	}
	if suffixed := SuffixedFilename(got, strings.Repeat("a", 64)); len(suffixed) > 255 {
		t.Errorf("suffixed filename is %d bytes, want at most 255", len(suffixed))
	}
}
