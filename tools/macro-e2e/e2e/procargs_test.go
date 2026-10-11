package e2e

import (
	"encoding/binary"
	"testing"
)

func procArgs(argc uint32, rest string) []byte {
	buf := make([]byte, 4, 4+len(rest))
	binary.NativeEndian.PutUint32(buf, argc)
	return append(buf, rest...)
}

func TestExec_PathFromProcArgs_ReadsThePathTheProcessWasExecedWith(t *testing.T) {
	buf := procArgs(2, "/var/folders/x/state/bin/waas-e2e-bin\x00\x00\x00\x00/var/folders/x/state/bin/waas-e2e-bin\x0030\x00")

	path, ok := execPathFromProcArgs(buf)

	if !ok || path != "/var/folders/x/state/bin/waas-e2e-bin" {
		t.Fatalf("execPathFromProcArgs = %q, %v", path, ok)
	}
}

func TestExec_PathFromProcArgs_RefusesATruncatedBuffer(t *testing.T) {
	for name, buf := range map[string][]byte{
		"empty":        nil,
		"argc only":    procArgs(1, ""),
		"no path":      procArgs(1, "\x00rest"),
		"unterminated": procArgs(1, "/bin/sleep"),
	} {
		if path, ok := execPathFromProcArgs(buf); ok {
			t.Errorf("%s: execPathFromProcArgs = %q, want refused", name, path)
		}
	}
}
