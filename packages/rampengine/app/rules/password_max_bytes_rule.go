package rules

import (
	"context"

	"github.com/goravel/framework/contracts/validation"
)

// PasswordMaxBytesRule caps a password at the 72 bytes bcrypt hashes: it refuses
// anything longer, so the hash failed and the request answered 500. max_len
// counts characters, and a multibyte letter spends more than one byte.
type PasswordMaxBytesRule struct{}

func (r *PasswordMaxBytesRule) Signature() string { return "password_max_bytes" }

func (r *PasswordMaxBytesRule) Passes(_ context.Context, _ validation.Data, val any, _ ...any) bool {
	s, ok := val.(string)
	return ok && len(s) <= 72
}

func (r *PasswordMaxBytesRule) Message(_ context.Context) string {
	return "password must be at most 72 bytes"
}
