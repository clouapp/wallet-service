package rules

import (
	"context"

	"github.com/goravel/framework/contracts/validation"
)

// CnpjRule accepts a Brazilian CNPJ however it is typed: numeric or
// alphanumeric (since 31/07/2026), with or without its mask, its letters in
// either case. A caller reads the value through brazil.CanonicalDocument.
type CnpjRule struct{}

func (r *CnpjRule) Signature() string { return "cnpj" }

func (r *CnpjRule) Passes(_ context.Context, _ validation.Data, val any, _ ...any) bool {
	value, ok := val.(string)
	return ok && isValidTypedCNPJ(value)
}

func (r *CnpjRule) Message(_ context.Context) string {
	return "must be a valid CNPJ"
}
