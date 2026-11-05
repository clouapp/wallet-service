package ramps

import (
	contractshttp "github.com/goravel/framework/contracts/http"

	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/http/requests"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/rules"

	kycsvc "github.com/macrowallets/waas/packages/rampengine/app/services/client/kyc"
)

// SubmitApplicationRequest validates POST and PATCH
// /api/kyc/{provider}/applications.
//
// The document images are deliberately not here. This body carries the profile
// only, which keeps it small enough for the request body limit; the images go
// to /documents, where they are capped per file.
type SubmitApplicationRequest struct {
	requests.Open
	Provider          string `form:"provider" json:"provider"`
	SignedAgreementID string `form:"signed_agreement_id" json:"signed_agreement_id"`
	ApplicationFields
}

// ApplicationFields is the profile both application endpoints carry. The
// fan-out embeds it too, so the two cannot drift apart; provider and
// signed_agreement_id stay out of it, on SubmitApplicationRequest alone (see
// SubmitApplicationFanOutRequest for why).
type ApplicationFields struct {
	// Type is accepted only as "individual", what installed builds send: the
	// type of an application is the customer's, never the client's, and a
	// company applies with its own form (KYB spec §7).
	Type         string   `form:"type" json:"type"`
	Endorsements []string `form:"endorsements" json:"endorsements"`
	FirstName    string   `form:"first_name" json:"first_name"`
	LastName     string   `form:"last_name" json:"last_name"`
	MotherName   string   `form:"mother_name" json:"mother_name"`
	Email        string   `form:"email" json:"email"`
	Phone        string   `form:"phone" json:"phone"`
	BirthDate    string   `form:"birth_date" json:"birth_date"`
	TaxID        string   `form:"tax_id" json:"tax_id"`
	// TaxIDType and TaxIDCountry say what TaxID is: a cpf (issued by BRA) or
	// a passport and the ISO alpha-3 country that issued it. Both optional: a
	// build that predates them sends neither and always country=BRA, and the
	// identity is derived from the residence (models.DeriveTaxIdentity).
	TaxIDType    string `form:"tax_id_type" json:"tax_id_type"`
	TaxIDCountry string `form:"tax_id_country" json:"tax_id_country"`
	StreetLine1  string `form:"street_line_1" json:"street_line_1"`
	StreetLine2  string `form:"street_line_2" json:"street_line_2"`
	Number       string `form:"number" json:"number"`
	Neighborhood string `form:"neighborhood" json:"neighborhood"`
	City         string `form:"city" json:"city"`
	Subdivision  string `form:"subdivision" json:"subdivision"`
	PostalCode   string `form:"postal_code" json:"postal_code"`
	Country      string `form:"country" json:"country"`
	// Risk profile, all optional: a build that predates them sends nothing and
	// the provider reports the gap through requirements. Keys, not labels —
	// see app/services/client/kyc/profile_vocabulary.go.
	Nationality               string `form:"nationality" json:"nationality"`
	Occupation                string `form:"occupation" json:"occupation"`
	SourceOfFunds             string `form:"source_of_funds" json:"source_of_funds"`
	AccountPurpose            string `form:"account_purpose" json:"account_purpose"`
	MonthlyIncomeBand         string `form:"monthly_income_band" json:"monthly_income_band"`
	ExpectedMonthlyVolumeBand string `form:"expected_monthly_volume_band" json:"expected_monthly_volume_band"`
	// IsPEP is a pointer so "false" and "absent" stay distinct — see
	// ramp.Profile.IsPEP.
	IsPEP *bool `form:"is_pep" json:"is_pep"`
}

// Input copies the profile into the service's input as the client sent it;
// canonicalising it is the service's job.
func (f ApplicationFields) Input() dto.ApplicationDTO {
	return dto.ApplicationDTO{
		Endorsements:              f.Endorsements,
		FirstName:                 f.FirstName,
		LastName:                  f.LastName,
		MotherName:                f.MotherName,
		Email:                     f.Email,
		Phone:                     f.Phone,
		BirthDate:                 f.BirthDate,
		TaxID:                     f.TaxID,
		TaxIDType:                 f.TaxIDType,
		TaxIDCountry:              f.TaxIDCountry,
		StreetLine1:               f.StreetLine1,
		StreetLine2:               f.StreetLine2,
		Number:                    f.Number,
		Neighborhood:              f.Neighborhood,
		City:                      f.City,
		Subdivision:               f.Subdivision,
		PostalCode:                f.PostalCode,
		Country:                   f.Country,
		Nationality:               f.Nationality,
		Occupation:                f.Occupation,
		SourceOfFunds:             f.SourceOfFunds,
		AccountPurpose:            f.AccountPurpose,
		MonthlyIncomeBand:         f.MonthlyIncomeBand,
		ExpectedMonthlyVolumeBand: f.ExpectedMonthlyVolumeBand,
		IsPEP:                     f.IsPEP,
	}
}

// Input is the profile plus the agreement id only this endpoint carries.
func (r SubmitApplicationRequest) Input() dto.ApplicationDTO {
	input := r.ApplicationFields.Input()
	input.SignedAgreementID = r.SignedAgreementID
	return input
}

// Rules reads the declared country off the request: the postal code and the
// subdivision are required by the country the address is in, and a custom
// rule never runs on an empty value, so that cannot be its job.
func (r *SubmitApplicationRequest) Rules(ctx contractshttp.Context) map[string]string {
	var country string
	if ctx != nil {
		country = ctx.Request().Input("country")
	}
	// Installed builds always send country=BRA and show the 422's message
	// verbatim (Android XipErrorResolver, iOS XipApiError). For the spellings
	// the old rule matched, the three fields keep it byte for byte — rule key
	// required_if, "tax_id is required when country is in [BRA,BR,USA,US]" —
	// rather than the plain required message. An empty country keeps it too:
	// it did not fire then (country itself is refused), and it does not now.
	required := "required|"
	legacy := country == "" || country == "BRA" || country == "BR"
	if legacy {
		required = "required_if:country,BRA,BR,USA,US|"
	}
	postalCode := "bridge_postal_code"
	if rules.PostalCodeRequired(country) {
		postalCode = required + postalCode
	}
	// max_len bounds the value where bridge_subdivision ignores it: those
	// countries took three characters at most before, and the value still
	// reaches the service, which drops it before Bridge (subdivisionFor).
	// Brazil's table stays as it was: the UF rule bounds it.
	subdivision := "max_len:64|bridge_subdivision"
	switch {
	case legacy:
		subdivision = required + "bridge_subdivision"
	case rules.SubdivisionRequired(country):
		subdivision = required + subdivision
	}
	return map[string]string{
		"provider": ProviderRule,
		"type":     "in:" + models.CustomerTypeIndividual,
		// Optional at the HTTP layer: ProviderKycService fills from the stored
		// row when the client resubmits after ToS was already accepted.
		"signed_agreement_id": "max_len:64",
		"first_name":          "required|max_len:64",
		"last_name":           "required|max_len:64",
		"email":               "required|email",
		// kyc_phone accepts what a client actually types — bare national
		// digits for the declared country, human separators, or E.164 —
		// and refuses only what the KYC service's applicationFrom cannot
		// canonicalise into the E.164 the contract promises. Refusing it
		// here names `phone`; refusing it downstream names a provider field
		// the client never sent.
		"phone": "max_len:32|kyc_phone",
		// ISO 8601 date, which is what every provider's customer payload takes.
		"birth_date": "required|kyc_birth_date",
		// kyc_tax_id reads tax_id_type, tax_id_country and country: a resident
		// of Brazil declares a CPF, of the United States an SSN or an ITIN,
		// anyone else a CPF or a passport.
		"tax_id":         required + "kyc_tax_id",
		"tax_id_type":    kycsvc.RuleIn(models.PersonTaxIDTypes),
		"tax_id_country": "len:3|kyc_country",
		"street_line_1":  "required|min_len:4|max_len:128|kyc_not_po_box",
		// Optional, but a US residence cannot hide a PO box in it either.
		"street_line_2": "kyc_not_po_box",
		// Optional on purpose: the app in production does not send them yet,
		// and making them required here would reject every submission from a
		// build already on someone's phone. A provider that needs them reports
		// the customer as incomplete, which is visible and recoverable.
		//
		// Ozav marks all three required for Brazilian onboarding, so they are
		// the fields most likely to come back in `requirements` — which is the
		// mechanism that works, and the reason not to enforce them here.
		"mother_name":  "max_len:128",
		"number":       "max_len:16",
		"neighborhood": "max_len:64",
		"city":         "required|min_len:1|max_len:64",
		"country":      "required|len:3|kyc_country",
		"subdivision":  subdivision,
		"postal_code":  postalCode,

		"nationality":                  "len:3|kyc_country",
		"occupation":                   "max_len:128",
		"source_of_funds":              kycsvc.RuleIn(kycsvc.SourceOfFundsKeys),
		"account_purpose":              kycsvc.RuleIn(kycsvc.AccountPurposeKeys),
		"monthly_income_band":          kycsvc.RuleIn(kycsvc.IncomeBandKeys),
		"expected_monthly_volume_band": kycsvc.RuleIn(kycsvc.VolumeBandKeys),
		// No `required`: the validator reads false as empty.
		"is_pep": "bool",
	}
}
