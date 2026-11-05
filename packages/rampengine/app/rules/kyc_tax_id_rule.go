package rules

import (
	"context"
	"regexp"
	"strings"

	"github.com/goravel/framework/contracts/validation"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/support/ustaxid"
)

// passportPattern is the shape a passport number is held to: letters and
// digits only, as printed in the machine-readable zone, 5 to 20 of them.
var passportPattern = regexp.MustCompile(`^[A-Za-z0-9]{5,20}$`)

// KycTaxIdRule validates the tax id against the identity the application
// declares: tax_id_type and tax_id_country, derived from the residence
// (country) when a build that predates them sends neither — so an installed
// build, which always sends BRA, is still read as a CPF. The three are read
// beside the tax id (sibling): a KYB partner's under its entry's scope.
//
// It also holds the combination of identity and residence
// (models.TaxIdentity.FitsResidence): a resident of Brazil declares a CPF, a
// resident of the United States an SSN or an ITIN, anyone else a CPF or a
// passport. Whether any provider serves the residence is not this rule's
// question: the adapters' jurisdiction policies answer it
// (jurisdiction_not_served).
type KycTaxIdRule struct{}

func (r *KycTaxIdRule) Signature() string { return "kyc_tax_id" }

func (r *KycTaxIdRule) Passes(_ context.Context, data validation.Data, val any, options ...any) bool {
	s, ok := val.(string)
	if !ok {
		return false
	}
	residence := kycCountryFromData(data, options)
	identity := models.DeriveTaxIdentity(sibling(data, options, "tax_id_type"), sibling(data, options, "tax_id_country"), residence)
	if !identity.FitsResidence(residence) {
		return false
	}
	return ValidTaxID(s, identity)
}

// ValidTaxID reports whether taxID is a valid document of identity: a CPF
// issued by Brazil with valid check digits, a passport number of shape, or an
// SSN or ITIN issued by the United States (ustaxid). Validating
// identity.Country as a country is the kyc_country rule's job.
func ValidTaxID(taxID string, identity models.TaxIdentity) bool {
	taxID = strings.TrimSpace(taxID)
	switch identity.Type {
	case models.TaxIDTypeCPF:
		return identity.Country == "BRA" && isValidCPF(taxID)
	case models.TaxIDTypePassport:
		return passportPattern.MatchString(taxID)
	case models.TaxIDTypeSSN:
		return identity.Country == "USA" && ustaxid.ValidSSN(taxID)
	case models.TaxIDTypeITIN:
		return identity.Country == "USA" && ustaxid.ValidITIN(taxID)
	default:
		return false
	}
}

func (r *KycTaxIdRule) Message(_ context.Context) string {
	return "must be a valid tax identifier for the selected country"
}
