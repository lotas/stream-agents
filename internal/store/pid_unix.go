//go:build !windows

package store

import (
	"errors"
	"syscall"
)

// pidAlive reports whether a process with pid exists. EPERM means it exists
// but belongs to another user.
func pidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
