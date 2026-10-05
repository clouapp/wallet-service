package features

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestClaudeRecordsFlagsAsPlatformKillSwitches(t *testing.T) {
	t.Parallel()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller is required")
	}
	body, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "CLAUDE.md"))
	if err != nil {
		t.Fatalf("read CLAUDE.md: %v", err)
	}
	text := strings.Join(strings.Fields(string(body)), " ")
	for _, phrase := range []string{
		"Flags are platform-controlled rollout and kill switches.",
		"Customer-controlled toggles are account settings.",
		"`account_security.require_2fa`",
		"`user-2fa-required` flag",
	} {
		if !strings.Contains(text, phrase) {
			t.Fatalf("CLAUDE.md missing %q", phrase)
		}
	}
}
