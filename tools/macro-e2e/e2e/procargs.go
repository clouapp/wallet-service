package e2e

import "bytes"

// procArgsCountSize is the native-endian int32 argc that opens a KERN_PROCARGS2 buffer.
const procArgsCountSize = 4

// execPathFromProcArgs is the path a process was exec'd with, read from a macOS
// KERN_PROCARGS2 buffer: argc, then that path NUL-terminated, then padding and the
// arguments. It refuses a buffer cut before the path ends.
func execPathFromProcArgs(buf []byte) (string, bool) {
	if len(buf) <= procArgsCountSize {
		return "", false
	}
	rest := buf[procArgsCountSize:]
	end := bytes.IndexByte(rest, 0)
	if end <= 0 {
		return "", false
	}
	return string(rest[:end]), true
}
