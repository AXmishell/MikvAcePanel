//go:build noweb

// Package webui provides an empty filesystem when the frontend is deployed
// separately (for example on a CDN) and the server is built with -tags noweb.
package webui

import (
	"errors"
	"io/fs"
)

type emptyFS struct{}

func (emptyFS) Open(string) (fs.File, error) {
	return nil, errors.New("web UI is not embedded in this build")
}

// FS returns an empty filesystem.
func FS() fs.FS { return emptyFS{} }
