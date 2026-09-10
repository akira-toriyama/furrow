//go:build !unix

package claudecode

import "os"

// processAlive on a non-unix host: os.FindProcess opens the process and fails
// when the pid does not exist.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	_, err := os.FindProcess(pid)
	return err == nil
}
