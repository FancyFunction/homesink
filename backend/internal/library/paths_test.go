package library

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newYearsEveMs is 23:30 on 31 December 2025 as experienced two hours east of
// UTC — the instant D-11 is written about.
const newYearsEveMs = int64(1_767_216_600_000)

// TestBuildRelPathAppliesCaptureOffsetBeforeYearMonth is the D-11 acceptance
// criterion: the offset decides the calendar, not the server's zone and not UTC.
//
// The criterion as written in WP-B5 says "the same instant at -600 lands in
// 2026/01". Minus 600 minutes is ten hours *behind* UTC, so that instant is
// 11:30 on 31 December there and cannot be January; +600 is the offset that
// crosses the year. Both are asserted below, so the direction the criterion is
// really about — one instant, two calendars — is covered either way.
func TestBuildRelPathAppliesCaptureOffsetBeforeYearMonth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		offsetMin int
		want      string
	}{
		{"berlin_summer_offset_stays_in_december", 120, "Camera/2025/12/IMG_0001.jpg"},
		{"ten_hours_west_stays_in_december", -600, "Camera/2025/12/IMG_0001.jpg"},
		{"ten_hours_east_crosses_into_january", 600, "Camera/2026/01/IMG_0001.jpg"},
		{"utc_stays_in_december", 0, "Camera/2025/12/IMG_0001.jpg"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := BuildRelPath("Camera", newYearsEveMs, tc.offsetMin, "IMG_0001.jpg")
			if got != tc.want {
				t.Errorf("BuildRelPath(offset %+d) = %q, want %q", tc.offsetMin, got, tc.want)
			}
		})
	}
}

// TestBuildRelPathIgnoresServerTimezone proves the derivation reads the offset
// and nothing else: the same call has to produce the same path whatever the
// process's local zone happens to be.
func TestBuildRelPathIgnoresServerTimezone(t *testing.T) {
	original := time.Local
	t.Cleanup(func() { time.Local = original })

	const want = "Camera/2025/12/IMG_0001.jpg"
	for _, zone := range []*time.Location{time.UTC, time.FixedZone("Pacific", -11*3600), time.FixedZone("Kiritimati", 14*3600)} {
		time.Local = zone
		if got := BuildRelPath("Camera", newYearsEveMs, 120, "IMG_0001.jpg"); got != want {
			t.Errorf("with server zone %v: BuildRelPath = %q, want %q", zone, got, want)
		}
	}
}

// TestBuildRelPathZeroPadsMonth is why 2025/03 sorts next to 2025/12 in a plain
// file manager (D-11).
func TestBuildRelPathZeroPadsMonth(t *testing.T) {
	t.Parallel()

	march := time.Date(2025, time.March, 5, 12, 0, 0, 0, time.UTC).UnixMilli()
	if got := BuildRelPath("Camera", march, 0, "a.jpg"); got != "Camera/2025/03/a.jpg" {
		t.Errorf("BuildRelPath = %q, want %q", got, "Camera/2025/03/a.jpg")
	}
}

// TestBuildRelPathSanitisesBothComponents checks that the album and the
// filename go through D-10 on the way in, so no caller can build a path around
// the sanitisers by calling this function directly.
func TestBuildRelPathSanitisesBothComponents(t *testing.T) {
	t.Parallel()

	got := BuildRelPath("../../etc", newYearsEveMs, 120, "../../../etc/passwd")
	if got != "etc/2025/12/passwd" {
		t.Errorf("BuildRelPath = %q, want %q", got, "etc/2025/12/passwd")
	}
	if strings.Contains(got, "..") {
		t.Errorf("BuildRelPath leaked a traversal segment: %q", got)
	}
}

// TestSuffixedFilenameIsDeterministic is the retry-safety half of D-13: the
// suffix is a pure function of the name and the hash, so a retried commit
// re-derives the same name instead of fanning out into _1, _2, _3.
func TestSuffixedFilenameIsDeterministic(t *testing.T) {
	t.Parallel()

	const hash = "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90"
	tests := []struct{ in, want string }{
		{"IMG_0001.jpg", "IMG_0001__a1b2c3d4.jpg"},
		{"archive.tar.gz", "archive.tar__a1b2c3d4.gz"},
		{"noextension", "noextension__a1b2c3d4"},
	}
	for _, tc := range tests {
		first := SuffixedFilename(tc.in, hash)
		if first != tc.want {
			t.Errorf("SuffixedFilename(%q) = %q, want %q", tc.in, first, tc.want)
		}
		if second := SuffixedFilename(tc.in, hash); second != first {
			t.Errorf("SuffixedFilename(%q) is not deterministic: %q then %q", tc.in, first, second)
		}
	}
}

// TestSafeAbsRejectsEscapes is the containment check WP-B5 requires "after all
// string manipulation". It is fed raw relative paths, bypassing the sanitisers,
// because it has to hold even if a future caller builds a path some other way.
func TestSafeAbsRejectsEscapes(t *testing.T) {
	t.Parallel()

	root := filepath.Clean(t.TempDir())
	escapes := []string{
		"../outside.jpg",
		"../../etc/passwd",
		"Camera/../../outside.jpg",
		"Camera/2025/12/../../../../outside.jpg",
		"..",
		".",
		"",
		"/etc/passwd",
		root + "/../outside.jpg",
	}
	for _, rel := range escapes {
		if got, err := safeAbs(root, rel); err == nil {
			t.Errorf("safeAbs(%q) = %q, want ErrUnsafePath", rel, got)
		} else if !errors.Is(err, ErrUnsafePath) {
			t.Errorf("safeAbs(%q) returned %v, want ErrUnsafePath", rel, err)
		}
	}

	abs, err := safeAbs(root, "Camera/2025/12/IMG_0001.jpg")
	if err != nil {
		t.Fatalf("safeAbs on a legitimate path: %v", err)
	}
	if want := filepath.Join(root, "Camera", "2025", "12", "IMG_0001.jpg"); abs != want {
		t.Errorf("safeAbs = %q, want %q", abs, want)
	}
}
