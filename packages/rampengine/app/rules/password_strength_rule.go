package rules

import (
	"context"
	"unicode"

	"github.com/goravel/framework/contracts/validation"
)

// PasswordStrengthRule requires at least one lowercase and one uppercase
// letter. Minimum length is enforced separately by `min_len`.
//
// Implemented as a custom rule because gookit/validate uses Go's RE2 regex
// engine, which does not support lookahead (`(?=...)`); a single regex
// cannot express "contains both classes".
type PasswordStrengthRule struct{}

func (r *PasswordStrengthRule) Signature() string { return "password_strength" }

func (r *PasswordStrengthRule) Passes(_ context.Context, _ validation.Data, val any, _ ...any) bool {
	s, ok := val.(string)
	if !ok {
		return false
	}
	var hasLower, hasUpper bool
	for _, c := range s {
		switch {
		case unicode.IsLower(c):
			hasLower = true
		case unicode.IsUpper(c):
			hasUpper = true
		}
		if hasLower && hasUpper {
			return true
		}
	}
	return false
}

func (r *PasswordStrengthRule) Message(_ context.Context) string {
	return "password must contain at least one lowercase and one uppercase letter"
}
