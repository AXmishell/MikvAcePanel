//go:build unix

package agent

import (
	"os"
	"syscall"
)

// restartProcess replaces the current agent process image with a fresh copy of
// the same binary. It keeps the PID, so Docker/systemd do not need to restart
// the container or service.
func restartProcess() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	argv := append([]string{exe}, os.Args[1:]...)
	return syscall.Exec(exe, argv, os.Environ())
}
