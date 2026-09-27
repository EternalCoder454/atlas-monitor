package process

import (
	"os"
	"strconv"
)

// ExecutablePath is the full path to a process's program file, or "" if it cannot
// be read.
//
// /proc/[pid]/exe is a symlink to the binary. Reading it needs the same ownership
// the rest of this package does, so a process belonging to another user gives "".
func ExecutablePath(pid int) string {
	exe, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/exe")
	if err != nil {
		return ""
	}
	return exe
}
