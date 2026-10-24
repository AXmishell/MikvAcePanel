//go:build !unix

package main

import "errors"

// restartProcess is unavailable on non-Unix builds; the installer falls back
// to asking the operator to restart the panel manually.
func restartProcess() error {
	return errors.New("automatic restart is not supported on this platform; restart the panel manually")
}
