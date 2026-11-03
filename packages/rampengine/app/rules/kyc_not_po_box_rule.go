package rules

import (
	"context"
	"regexp"

	"github.com/goravel/framework/contracts/validation"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// poBoxPattern is a post office box as an address line spells it: "PO Box",
// "P.O. Box", "POBox" or "Post Office Box", in any case.
var poBoxPattern = regexp.MustCompile(`(?i)\bP\.?\s*O\.?\s*BOX\b|\bPOST\s+OFFICE\s+BOX\b`)

// KycNotPoBoxRule refuses a post office box as a residential address line in
// the United States: Bridge answers 400 "residential_address: cannot be a PO
// box" (sandbox, 14/10/2026), which the worker could only report after the
// form was left. Anywhere else the line passes: no provider has been seen to
// refuse one there.
type KycNotPoBoxRule struct{}

func (r *KycNotPoBoxRule) Signature() string { return "kyc_not_po_box" }

func (r *KycNotPoBoxRule) Passes(_ context.Context, data validation.Data, val any, options ...any) bool {
	s, ok := val.(string)
	if !ok {
		return false
	}
	return !IsPoBox(s, kycCountryFromData(data, options))
}

// IsPoBox reports whether line is a post office box in a country that refuses
// one as a residence (the United States). An empty country is Brazil.
func IsPoBox(line, country string) bool {
	return models.ResidenceCode(country) == "USA" && poBoxPattern.MatchString(line)
}

func (r *KycNotPoBoxRule) Message(_ context.Context) string {
	return "must be a physical address, not a PO Box"
}
