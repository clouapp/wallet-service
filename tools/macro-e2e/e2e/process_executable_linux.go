//go:build linux

package e2e

import (
	"os"
	"strconv"
	"strings"
)

// deletedExeSuffix is what /proc/<pid>/exe gains once the binary was replaced on disk.
const deletedExeSuffix = " (deleted)"

// ProcessExecutable is readlink(/proc/<pid>/exe) without " (deleted)"; false once the
// process is gone (or a zombie).
func ProcessExecutable(pid int) (string, bool) {
	executable, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/exe")
	if err != nil {
		return "", false
	}
	return strings.TrimSuffix(executable, deletedExeSuffix), true
}
