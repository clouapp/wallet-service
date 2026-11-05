package rules

import (
	"context"

	"github.com/goravel/framework/contracts/validation"

	"github.com/macrowallets/waas/packages/rampengine/app/support/iso3166"
)

// KycCountryRule accepts an ISO 3166-1 country code, alpha-2 or alpha-3 in any
// case. The form requests pair it with len:3, because a country travels on
// the wire as alpha-3; the rule itself answers only "is this a country".
type KycCountryRule struct{}

func (r *KycCountryRule) Signature() string { return "kyc_country" }

func (r *KycCountryRule) Passes(_ context.Context, _ validation.Data, val any, _ ...any) bool {
	s, ok := val.(string)
	if !ok {
		return false
	}
	_, ok = iso3166.Alpha3(s)
	return ok
}

func (r *KycCountryRule) Message(_ context.Context) string {
	return "must be an ISO 3166-1 country code"
}
