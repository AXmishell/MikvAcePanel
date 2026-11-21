//go:build !unix

package agent

import "errors"

// restartProcess is unavailable on non-Unix builds. The command handler keeps
// the cores running and reports the error instead of exiting.
func restartProcess() error {
	return errors.New("agent self-restart is not supported on this platform")
}
