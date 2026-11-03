package rules

import (
	"context"
	"strings"
	"testing"
)

// bcrypt refuses more than 72 bytes, and a multibyte letter spends two of them,
// so the limit is on bytes, not on the characters max_len counts.
func TestPasswordMaxBytesRule_Passes(t *testing.T) {
	r := &PasswordMaxBytesRule{}
	for _, tc := range []struct {
		name  string
		val   any
		wants bool
	}{
		{"72 ascii bytes", strings.Repeat("a", 72), true},
		{"73 ascii bytes", strings.Repeat("a", 73), false},
		{"36 two-byte letters are 72 bytes", strings.Repeat("ç", 36), true},
		{"37 two-byte letters are 74 bytes", strings.Repeat("ç", 37), false},
		{"not a string", 123, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := r.Passes(context.Background(), mapData{}, tc.val); got != tc.wants {
				t.Errorf("Passes(%v) = %v, want %v", tc.val, got, tc.wants)
			}
		})
	}
}
