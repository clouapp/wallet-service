package rules

import (
	"context"

	"github.com/goravel/framework/contracts/validation"

	"github.com/macrowallets/waas/pkg/numeric"
)

// Uint32 accepts an integer from 0 to 4294967295, as a JSON number or a decimal
// string (form and query values). A missing or null value passes (use "required"
// to demand one).
type Uint32 struct{}

func (r *Uint32) Signature() string {
	return "uint32"
}

func (r *Uint32) Passes(_ context.Context, _ validation.Data, val any, _ ...any) bool {
	_, ok := numeric.ParseUint32(val)
	return ok || val == nil
}

func (r *Uint32) Message(_ context.Context) string {
	return "The :attribute must be an integer from 0 to 4294967295."
}
