// Package version exposes build metadata.
package version

import "runtime"

var (
	// Version is the release version, overridden at build time.
	Version = "dev"
	// Commit is the git commit, overridden at build time.
	Commit = "unknown"
	// BuildDate is the build timestamp, overridden at build time.
	BuildDate = "unknown"
)

// String returns a compact version string.
func String() string {
	if Commit == "" || Commit == "unknown" {
		return Version
	}
	if len(Commit) > 8 {
		return Version + "+" + Commit[:8]
	}
	return Version + "+" + Commit
}

// Platform returns the runtime platform.
func Platform() string { return runtime.GOOS + "/" + runtime.GOARCH }
