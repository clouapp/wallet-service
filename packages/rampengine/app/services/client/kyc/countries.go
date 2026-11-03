package kyc

import (
	"context"
	"slices"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/support/iso3166"
)

// Form profiles: which of the app's hand-built forms a residence gets. "us"
// is the generic form with US labels (State, ZIP code) and a US tax id; a
// build that predates it reads it as generic.
const (
	FormProfileBR      = "br"
	FormProfileUS      = "us"
	FormProfileGeneric = "generic"
)

// documentTypes are the identity documents that prove each tax identity: a
// CPF holder shows a Brazilian ID (RG), a driver's licence (CNH) or a
// passport; a passport holder, the passport; a resident of the United States
// a US driver's license, a state ID or a passport (Bridge took all three as
// government_id_document, sandbox 14/10/2026). The license comes first: the
// app preselects the first, and it is the one most residents carry.
var documentTypes = map[string][]string{
	models.TaxIDTypeCPF:      {"national_id", "drivers_license", "passport"},
	models.TaxIDTypePassport: {"passport"},
	models.TaxIDTypeSSN:      {"drivers_license", "state_or_provincial_id", "passport"},
	models.TaxIDTypeITIN:     {"drivers_license", "state_or_provincial_id", "passport"},
}

// Countries is the catalog the app builds its per-country KYC form from:
// every ISO 3166-1 country where some eligible provider opens a rail to some
// tax identity, a person's or a company's, sorted by code, with the address
// facts its form needs.
//
// The providers are narrowed the way List and the fan-out narrow them —
// enabled, in the mode APP_ENV expects, implemented (implementedProviders) —
// so the catalog offers nothing a submission would not reach. Quiet on a mode
// mismatch: this runs on every form load, and the fan-out logs it when it
// matters. No cache: the work is in memory, and an operator switching a
// provider is felt at once. A residence a feature flag holds shut
// (kycflags.ResidenceFlag) offers no person's identity, and neither does one
// whose flag cannot be read — logged, as capabilities and requireRail close
// only what they cannot read. Its companies are offered either way: the flag
// is a person's (refuseClosedResidence), so a country can be listed for its
// companies alone, with no person's identity, which the person's form skips.
func (s *ProviderKycService) Countries(ctx context.Context) ([]dto.CountryDTO, error) {
	adapters, err := implementedProviders(s.config, s.factory, s.appEnv, nil)
	if err != nil {
		return nil, err
	}
	out := []dto.CountryDTO{}
	for _, country := range iso3166.Countries() {
		var options []dto.TaxIdentityOptionDTO
		open, err := s.residenceOpen(ctx, country.Alpha3)
		if err != nil {
			facades.Log().WithContext(ctx).Errorf("kyc: countries: %v (offering no person in %s)", err, country.Alpha3)
		}
		if open {
			options = taxIdentityOptions(adapters, country.Alpha3)
		}
		business := businessTaxIdentityOptions(adapters, country.Alpha3)
		if len(options) == 0 && len(business) == 0 {
			continue
		}
		out = append(out, dto.CountryDTO{
			Code: country.Alpha3, Alpha2: country.Alpha2, FormProfile: formProfile(country.Alpha3),
			PostalCodePattern:     iso3166.PostalCodePattern(country.Alpha3),
			Subdivisions:          iso3166.RequiredSubdivisions(country.Alpha3),
			TaxIdentities:         options,
			BusinessTaxIdentities: business,
			CompanyTypes:          offeredCompanyTypes(business, country.Alpha3),
		})
	}
	return out, nil
}

func formProfile(residence string) string {
	switch residence {
	case "BRA":
		return FormProfileBR
	case "USA":
		return FormProfileUS
	}
	return FormProfileGeneric
}

// taxIdentitiesFor is what a resident of residence may declare, in the order
// the KYC form offers it: a CPF in Brazil; an SSN or an ITIN in the United
// States; a CPF or a passport anywhere else, the passport previewed as one the
// residence issued (the form picks its real issuer).
func taxIdentitiesFor(residence string) []models.TaxIdentity {
	switch code := models.ResidenceCode(residence); code {
	case "BRA":
		return []models.TaxIdentity{{Type: models.TaxIDTypeCPF, Country: "BRA"}}
	case "USA":
		return []models.TaxIdentity{{Type: models.TaxIDTypeSSN, Country: "USA"}, {Type: models.TaxIDTypeITIN, Country: "USA"}}
	default:
		return []models.TaxIdentity{{Type: models.TaxIDTypeCPF, Country: "BRA"}, {Type: models.TaxIDTypePassport, Country: code}}
	}
}

// taxIdentityOptions are the identities a resident may declare
// (taxIdentitiesFor), each with the rails it opens and the providers that open
// them; one that opens none is left out.
func taxIdentityOptions(adapters []ramp.ProviderAdapter, residence string) []dto.TaxIdentityOptionDTO {
	var out []dto.TaxIdentityOptionDTO
	for _, identity := range taxIdentitiesFor(residence) {
		rails, providers := openRails(adapters, identity, residence)
		if len(rails) == 0 {
			continue
		}
		out = append(out, dto.TaxIdentityOptionDTO{
			Type: identity.Type, Rails: rails, DocumentTypes: slices.Clone(documentTypes[identity.Type]),
			Providers: providers,
		})
	}
	return out
}

// businessTaxIdentityOptions are the identities a company incorporated in
// incorporation may declare (models.BusinessTaxIdentitiesFor), each with the
// rails it opens, the providers that open them — only business providers do
// (ramp.RailsOf) — and the company documents those providers want; one that
// opens none is left out.
func businessTaxIdentityOptions(adapters []ramp.ProviderAdapter, incorporation string) []dto.BusinessTaxIdentityOptionDTO {
	var out []dto.BusinessTaxIdentityOptionDTO
	for _, identity := range models.BusinessTaxIdentitiesFor(incorporation) {
		rails, providers := openRails(adapters, identity, incorporation)
		if len(rails) == 0 {
			continue
		}
		out = append(out, dto.BusinessTaxIdentityOptionDTO{
			Type: identity.Type, Rails: rails, Providers: providers,
			DocumentPurposes:               companyDocuments(adapters, providers, incorporation, true),
			DocumentPurposesWithoutWebsite: companyDocuments(adapters, providers, incorporation, false),
		})
	}
	return out
}

// companyDocuments is what the providers want from a company incorporated in
// incorporation that declares a website or none, once each, in
// ramp.CompanyDocumentPurposes order.
func companyDocuments(adapters []ramp.ProviderAdapter, providers []string, incorporation string, hasWebsite bool) []string {
	wanted := map[string]bool{}
	for _, adapter := range adapters {
		business, ok := ramp.BusinessKycOf(adapter)
		if !ok || !slices.Contains(providers, adapter.Code()) {
			continue
		}
		for _, purpose := range business.BusinessApplicationDocuments(incorporation, hasWebsite) {
			wanted[purpose] = true
		}
	}
	var out []string
	for _, purpose := range ramp.CompanyDocumentPurposes() {
		if wanted[purpose] {
			out = append(out, purpose)
		}
	}
	return out
}

// offeredCompanyTypes is models.CompanyTypesFor when some provider takes a company
// incorporated there, and nothing when none does.
func offeredCompanyTypes(business []dto.BusinessTaxIdentityOptionDTO, incorporation string) []string {
	if len(business) == 0 {
		return nil
	}
	return models.CompanyTypesFor(incorporation)
}

// openRails is the union of what each provider opens to identity, in
// ramp.AllRails order whatever order a provider lists them in, and the
// providers that open any, in adapter order.
func openRails(adapters []ramp.ProviderAdapter, identity models.TaxIdentity, residence string) (rails, providers []string) {
	open := make(map[ramp.Rail]bool, len(ramp.AllRails()))
	for _, adapter := range adapters {
		opened := ramp.RailsOf(adapter, identity, residence)
		if len(opened) > 0 {
			providers = append(providers, adapter.Code())
		}
		for _, rail := range opened {
			open[rail] = true
		}
	}
	for _, rail := range ramp.AllRails() {
		if open[rail] {
			rails = append(rails, rail.String())
		}
	}
	return rails, providers
}
