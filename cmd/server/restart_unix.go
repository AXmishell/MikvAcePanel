//go:build unix

package main

import (
	"os"
	"syscall"
)

// restartProcess replaces the current process image with a fresh copy of the
// panel. It keeps the PID and works under Docker, systemd and a plain shell.
func restartProcess() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	argv := append([]string{exe}, os.Args[1:]...)
	return syscall.Exec(exe, argv, os.Environ())
}
