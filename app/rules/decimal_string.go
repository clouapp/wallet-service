package rules

import (
	"context"
	"strings"

	"github.com/goravel/framework/contracts/validation"

	"github.com/macrowallets/waas/pkg/numeric"
)

type DecimalString struct{}

func (r *DecimalString) Signature() string {
	return "decimal_string"
}

// Passes accepts an exact decimal in plain or exponent notation; NaN, infinities,
// hexadecimal floats and surrounding spaces are rejected. Empty values pass (use
// "required" to demand one).
func (r *DecimalString) Passes(_ context.Context, _ validation.Data, val any, _ ...any) bool {
	s, ok := val.(string)
	if !ok || s == "" {
		return true
	}
	if s != strings.TrimSpace(s) {
		return false
	}
	_, err := numeric.Parse("value", s)
	return err == nil
}

func (r *DecimalString) Message(_ context.Context) string {
	return "The :attribute must be a valid decimal number."
}
