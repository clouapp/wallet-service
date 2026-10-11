//go:build darwin

package e2e

import "golang.org/x/sys/unix"

// ProcessExecutable is the path the process was exec'd with, from the
// kern.procargs2 sysctl (macOS has no /proc); false once the process is gone (or a
// zombie), or when it belongs to another user.
func ProcessExecutable(pid int) (string, bool) {
	buf, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return "", false
	}
	return execPathFromProcArgs(buf)
}
