package rules

import (
	"github.com/goravel/framework/contracts/validation"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// kycCountryFromData is the residence the KYC request declares, normalised
// by models.ResidenceCode: its country field, beside the validated one
// (sibling).
func kycCountryFromData(data validation.Data, options []any) string {
	return models.ResidenceCode(sibling(data, options, "country"))
}

// sibling reads the string field key beside the one a rule validates: at the
// top of the request, or under the scope the rule was given as its option
// (kyc_tax_id:associated_persons.0) for a field nested in an object or a list
// entry — a KYB application's partners each declare their own residence and
// identity, its addresses their own country (API contract §5.2).
func sibling(data validation.Data, options []any, key string) string {
	if len(options) > 0 {
		if scope, ok := options[0].(string); ok && scope != "" {
			key = scope + "." + key
		}
	}
	return stringFromData(data, key)
}

// stringFromData reads a string field of the request being validated; ""
// when it is absent or not a string.
func stringFromData(data validation.Data, key string) string {
	if data == nil {
		return ""
	}
	value, _ := data.Get(key)
	s, _ := value.(string)
	return s
}
