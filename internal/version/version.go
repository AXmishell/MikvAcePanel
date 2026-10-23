// Package version exposes build metadata.
package version

import (
	"runtime"
	"strings"
)

var (
	// Version is the release version, overridden at build time.
	Version = "dev"
	// Commit is the git commit, overridden at build time.
	Commit = "unknown"
	// BuildDate is the build timestamp, overridden at build time.
	BuildDate = "unknown"
)

// ShortCommit returns the abbreviated commit hash, or "" when unavailable.
func ShortCommit() string {
	c := strings.TrimSpace(Commit)
	if c == "" || c == "unknown" {
		return ""
	}
	if len(c) > 8 {
		return c[:8]
	}
	return c
}

// String returns a compact version string. When the build version already
// embeds the commit (for example `git describe --always --dirty` produces
// "aba90dd-dirty"), the hash is not appended a second time.
func String() string {
	v := strings.TrimSpace(Version)
	if v == "" {
		v = "dev"
	}
	short := ShortCommit()
	if short == "" {
		return v
	}
	if strings.Contains(v, short) {
		return v
	}
	return v + "+" + short
}

// Platform returns the runtime platform.
func Platform() string { return runtime.GOOS + "/" + runtime.GOARCH }
