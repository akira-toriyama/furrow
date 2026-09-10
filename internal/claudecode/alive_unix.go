//go:build unix

package claudecode

import (
	"errors"
	"syscall"
)

// processAlive is `kill -0`: signal 0 delivers nothing and reports whether the
// pid exists. EPERM means it exists under another user, which is alive too.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false // 0 = the caller's process group, negative = a group: never a session
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
