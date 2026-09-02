// Package buildinfo exposes the version metadata stamped into the binary at
// link time. The zero values below are overridden with:
//
//	go build -ldflags "-X github.com/FancyFunction/homesink/backend/internal/buildinfo.Version=1.2.3 ..."
package buildinfo

import "runtime/debug"

// These variables are set via -ldflags at build time. They are deliberately
// plain strings so the linker can overwrite them; do not change their types.
var (
	// Version is the release version, e.g. "1.4.0". "dev" when built without ldflags.
	Version = "dev"
	// Commit is the git SHA the binary was built from. "unknown" when unset.
	Commit = "unknown"
	// BuildTime is an RFC3339 timestamp of the build. "unknown" when unset.
	BuildTime = "unknown"
)

// Info is a snapshot of the build metadata.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"buildTime"`
	GoVersion string `json:"goVersion"`
}

// Get returns the current build metadata, filling Commit and GoVersion from the
// embedded Go build info when they were not provided via -ldflags.
func Get() Info {
	info := Info{
		Version:   Version,
		Commit:    Commit,
		BuildTime: BuildTime,
		GoVersion: "unknown",
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return info
	}
	info.GoVersion = bi.GoVersion
	if info.Commit == "unknown" {
		for _, s := range bi.Settings {
			if s.Key == "vcs.revision" {
				info.Commit = s.Value
			}
		}
	}
	return info
}

// String renders the metadata as a single human-readable line.
func (i Info) String() string {
	return "homesinkd " + i.Version + " (commit " + i.Commit + ", built " + i.BuildTime + ", " + i.GoVersion + ")"
}
