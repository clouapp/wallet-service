package kyc_test

import (
	"context"
	"errors"
	"testing"

	"github.com/goravel/framework/testing/mock"
	"github.com/stretchr/testify/assert"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	kycflags "github.com/macrowallets/waas/packages/rampengine/app/featureflags/kyc"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/packages/featureflag/contracts"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
)

// businessBridgeLike is bridgeLike that also takes companies: both Pix rails
// for a CNPJ, the deposit for a company from abroad, and the company
// documents Bridge asks for.
type businessBridgeLike struct{ jurisdictionAdapter }

func (businessBridgeLike) BusinessApplicationDocuments(incorporation string, hasWebsite bool) []string {
	documents := []string{ramp.CompanyDocumentFormation, ramp.CompanyDocumentProofOfAddress, ramp.CompanyDocumentSourceOfFunds}
	if incorporation == "MEX" {
		documents = append(documents, ramp.CompanyDocumentTaxIdentification)
	}
	if !hasWebsite {
		documents = append(documents, ramp.CompanyDocumentNatureOfBusiness)
	}
	return documents
}

func newBusinessBridgeLike() businessBridgeLike {
	person := bridgeLike()
	return businessBridgeLike{jurisdictionAdapter{code: models.ProviderBridge, rails: func(identity ramp.TaxIdentity, residence string) []ramp.Rail {
		if !identity.IsBusiness() {
			return person.rails(identity, residence)
		}
		if residence == "CUB" || residence == "USA" || residence == "PRT" || !identity.FitsIncorporation(residence) {
			return nil
		}
		if identity.Type == models.TaxIDTypeCNPJ {
			return ramp.PixRails()
		}
		return []ramp.Rail{ramp.RailPixPayin}
	}}}
}

// Each country also says what a company incorporated there declares, with
// the rails and the providers that serve it and the company documents those
// providers want, and the company types its form offers (spec §7.2). The
// person's fields do not change.
func TestProviderKycService_Countries_ListWhatACompanyDeclares(t *testing.T) {
	svc := newCountriesService(t, newBusinessBridgeLike(), ozavLike())

	countries, err := svc.Countries(context.Background())
	require.NoError(t, err)

	companyDocuments := []string{"formation", "proof_of_address", "source_of_funds"}
	withoutWebsite := []string{"formation", "proof_of_address", "source_of_funds", "nature_of_business"}
	bra := countryByCode(countries, "BRA")
	require.NotNil(t, bra)
	assert.Equal(t, []dto.TaxIdentityOptionDTO{cpfOption(models.ProviderBridge, models.ProviderOzav)}, bra.TaxIdentities)
	assert.Equal(t, []dto.BusinessTaxIdentityOptionDTO{{
		Type: "cnpj", Rails: []string{models.RailPixPayin, models.RailPixPayout},
		Providers: []string{models.ProviderBridge}, DocumentPurposes: companyDocuments,
		DocumentPurposesWithoutWebsite: withoutWebsite,
	}}, bra.BusinessTaxIdentities, "Ozav takes no company")
	assert.Equal(t, []string{"mei", "ei", "slu", "ltda", "sa", "other"}, bra.CompanyTypes)

	gbr := countryByCode(countries, "GBR")
	require.NotNil(t, gbr)
	payin := []string{models.RailPixPayin}
	assert.Equal(t, []dto.BusinessTaxIdentityOptionDTO{
		{Type: "crn", Rails: payin, Providers: []string{models.ProviderBridge}, DocumentPurposes: companyDocuments,
			DocumentPurposesWithoutWebsite: withoutWebsite},
		{Type: "vat", Rails: payin, Providers: []string{models.ProviderBridge}, DocumentPurposes: companyDocuments,
			DocumentPurposesWithoutWebsite: withoutWebsite},
	}, gbr.BusinessTaxIdentities)
	assert.Equal(t, []string{"corporation", "llc", "partnership", "sole_prop", "cooperative", "trust", "other"}, gbr.CompanyTypes)

	mex := countryByCode(countries, "MEX")
	require.NotNil(t, mex)
	require.Len(t, mex.BusinessTaxIdentities, 2)
	assert.Equal(t, "rfc", mex.BusinessTaxIdentities[0].Type)
	assert.Equal(t, []string{"formation", "proof_of_address", "source_of_funds", "tax_identification"},
		mex.BusinessTaxIdentities[0].DocumentPurposes)
	assert.Equal(t, []string{"formation", "proof_of_address", "source_of_funds", "tax_identification", "nature_of_business"},
		mex.BusinessTaxIdentities[0].DocumentPurposesWithoutWebsite, "in the order the app asks them")
}

// A provider that takes no company leaves every country without company
// options and without company types: there is nobody to register one with.
func TestProviderKycService_Countries_NoBusinessProviderOffersNoCompany(t *testing.T) {
	svc := newCountriesService(t, bridgeLike(), ozavLike())

	countries, err := svc.Countries(context.Background())
	require.NoError(t, err)

	bra := countryByCode(countries, "BRA")
	require.NotNil(t, bra)
	assert.Empty(t, bra.BusinessTaxIdentities)
	assert.Empty(t, bra.CompanyTypes)
}

// servesAnyoneAndAnyCompany opens the payout to every person and the deposit
// to every company, everywhere: what the catalog leaves out is its own doing.
func servesAnyoneAndAnyCompany() businessBridgeLike {
	return businessBridgeLike{jurisdictionAdapter{code: models.ProviderBridge, rails: func(identity ramp.TaxIdentity, _ string) []ramp.Rail {
		if identity.IsBusiness() {
			return []ramp.Rail{ramp.RailPixPayin}
		}
		return []ramp.Rail{ramp.RailPixPayout}
	}}}
}

// A country is listed for its companies as well as for its people (PLANO
// B2): while the flag holds the United States shut to the people living
// there, a US company is still offered — the country with no person's
// identity, which the app's person form leaves out — and the flag is not
// read for it, so a store that cannot answer closes only the person's part.
func TestProviderKycService_Countries_ACompanyIsListedWhereNoPersonIs(t *testing.T) {
	failing := mocks.NewMockFeature(t)
	failing.EXPECT().IsActive(testifymock.Anything, kycflags.USAResidenceEnabled, nil).
		Return(false, errors.New("store down")).Once()
	for name, features := range map[string]contracts.Feature{
		"the flag off":   usaSwitch(t, false),
		"no flags wired": nil,
		"a store down":   failing,
	} {
		t.Run(name, func(t *testing.T) {
			mock.Factory().Log()
			svc := newCountriesServiceWith(t, features, servesAnyoneAndAnyCompany())

			countries, err := svc.Countries(context.Background())

			require.NoError(t, err)
			usa := countryByCode(countries, "USA")
			require.NotNil(t, usa, "a US company is served")
			assert.Empty(t, usa.TaxIdentities, "nobody living there is")
			require.Len(t, usa.BusinessTaxIdentities, 1)
			assert.Equal(t, models.TaxIDTypeEIN, usa.BusinessTaxIdentities[0].Type)
			assert.Equal(t, models.CompanyTypesFor("USA"), usa.CompanyTypes)
			assert.NotEmpty(t, countryByCode(countries, "BRA").TaxIdentities, "a residence no flag holds keeps its people")
		})
	}

	countries, err := newCountriesServiceWith(t, usaSwitch(t, true), servesAnyoneAndAnyCompany()).Countries(context.Background())
	require.NoError(t, err)
	assert.NotEmpty(t, countryByCode(countries, "USA").TaxIdentities, "the flag on opens its people too")
}
