package rules

import (
	"context"
	"regexp"
	"strings"

	"github.com/goravel/framework/contracts/validation"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

var (
	// einPattern is a US Employer Identification Number: nine digits, with
	// or without the dash after the second.
	einPattern = regexp.MustCompile(`^\d{2}-?\d{7}$`)
	// crnPattern is a UK company registration number: eight characters,
	// digits or the two-letter prefix of a Scottish or Northern Irish one.
	crnPattern = regexp.MustCompile(`^[A-Za-z0-9]{8}$`)
	// rfcPattern is a Mexican company's RFC: three letters (& and Ñ among
	// them), the six digits of its incorporation date and a three-character
	// homoclave. A person's RFC has a fourth letter and is no company's.
	rfcPattern = regexp.MustCompile(`(?i)^[A-Z&Ñ]{3}\d{6}[A-Z0-9]{3}$`)
)

// BusinessTaxIdRule validates a company's tax id against what the request
// declares of it, tax_id_type and incorporation_country (spec §7.2): it is
// the company's kyc_tax_id, which is a person's and never reads one.
//
// The type has to fit the country of incorporation
// (models.TaxIdentity.FitsIncorporation) and the number the shape of its type:
// a CNPJ with its check digits, numeric or alphanumeric, typed with or without
// its mask; an EIN of nine digits; a CRN of eight characters; an RFC of
// twelve; a VAT number or another registration alphanumeric, as a passport is.
// Whether any provider serves the country is not this rule's question: the
// jurisdiction policies answer it (jurisdiction_not_served).
type BusinessTaxIdRule struct{}

func (r *BusinessTaxIdRule) Signature() string { return "business_tax_id" }

func (r *BusinessTaxIdRule) Passes(_ context.Context, data validation.Data, val any, _ ...any) bool {
	taxID, ok := val.(string)
	if !ok {
		return false
	}
	country := stringFromData(data, "incorporation_country")
	identity := models.TaxIdentity{Type: strings.ToLower(strings.TrimSpace(stringFromData(data, "tax_id_type")))}
	if !identity.FitsIncorporation(country) {
		return false
	}
	return validBusinessTaxID(strings.TrimSpace(taxID), identity.Type)
}

func (r *BusinessTaxIdRule) Message(_ context.Context) string {
	return "must be a valid tax identifier of a company incorporated in the selected country"
}

func validBusinessTaxID(taxID, taxIDType string) bool {
	switch taxIDType {
	case models.TaxIDTypeCNPJ:
		return isValidTypedCNPJ(taxID)
	case models.TaxIDTypeEIN:
		return einPattern.MatchString(taxID)
	case models.TaxIDTypeCRN:
		return crnPattern.MatchString(taxID)
	case models.TaxIDTypeRFC:
		return rfcPattern.MatchString(taxID)
	case models.TaxIDTypeVAT, models.TaxIDTypeOther:
		return passportPattern.MatchString(taxID)
	default:
		return false
	}
}
