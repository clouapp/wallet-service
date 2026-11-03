package kyc_test

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/goravel/framework/testing/mock"
	"github.com/stretchr/testify/assert"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/featureflags/catalog"
	kycflags "github.com/macrowallets/waas/packages/rampengine/app/featureflags/kyc"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/kyc"
	"github.com/macrowallets/waas/packages/rampengine/app/support/iso3166"
	"github.com/macrowallets/waas/packages/rampengine/packages/featureflag"
	"github.com/macrowallets/waas/packages/rampengine/packages/featureflag/contracts"
	"github.com/macrowallets/waas/packages/rampengine/packages/featureflag/drivers"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
)

// jurisdictionAdapter is a provider that declares its jurisdiction policy.
// The catalog only asks it for its code and its rails, so the embedded
// interface stays nil: any other call would panic the test.
type jurisdictionAdapter struct {
	ramp.ProviderAdapter
	code  string
	rails func(identity ramp.TaxIdentity, residence string) []ramp.Rail
}

func (a jurisdictionAdapter) Code() ramp.ProviderCode { return a.code }

func (a jurisdictionAdapter) RailsFor(identity ramp.TaxIdentity, residence string) []ramp.Rail {
	return a.rails(identity, residence)
}

// stubJurisdictionAdapter is an enabled placeholder that would serve anyone:
// the catalog must pass it over, as the fan-out does.
type stubJurisdictionAdapter struct{ jurisdictionAdapter }

func (stubJurisdictionAdapter) IsStub() bool { return true }

// bridgeLike is Bridge's policy in miniature: CUB, USA and an EEA residence
// (PRT) refused as residences, both rails for a CPF issued by Brazil, the
// payout for a passport. It answers only for the passport the residence
// issued, which is the one the catalog previews, and lists the payout first
// so the test sees the catalog put the rails in order.
func bridgeLike() jurisdictionAdapter {
	return jurisdictionAdapter{code: models.ProviderBridge, rails: func(identity ramp.TaxIdentity, residence string) []ramp.Rail {
		if residence == "CUB" || residence == "USA" || residence == "PRT" {
			return nil
		}
		switch {
		case identity.Type == models.TaxIDTypeCPF && identity.Country == "BRA":
			return []ramp.Rail{ramp.RailPixPayout, ramp.RailPixPayin}
		case identity.Type == models.TaxIDTypePassport && identity.Country == residence:
			return []ramp.Rail{ramp.RailPixPayout}
		}
		return nil
	}}
}

// ozavLike is Ozav's: both rails for a CPF holder living in Brazil, nothing
// for anyone else.
func ozavLike() jurisdictionAdapter {
	return jurisdictionAdapter{code: models.ProviderOzav, rails: func(identity ramp.TaxIdentity, residence string) []ramp.Rail {
		if identity.Type == models.TaxIDTypeCPF && residence == "BRA" {
			return []ramp.Rail{ramp.RailPixPayin, ramp.RailPixPayout}
		}
		return nil
	}}
}

// newCountriesService enables exactly the given adapters' providers, in
// sandbox, and registers the adapters in the factory. It wires no flags, so
// the United States stays shut.
func newCountriesService(t *testing.T, adapters ...ramp.ProviderAdapter) *kyc.ProviderKycService {
	t.Helper()
	return newCountriesServiceWith(t, nil, adapters...)
}

// newCountriesServiceWith is newCountriesService reading features.
func newCountriesServiceWith(t *testing.T, features contracts.Feature, adapters ...ramp.ProviderAdapter) *kyc.ProviderKycService {
	t.Helper()
	enabled := make(map[string]bool, len(adapters))
	for _, adapter := range adapters {
		enabled[adapter.Code()] = true
	}
	settings := mocks.NewMockSettingsSource(t)
	for _, code := range ramp.AllProviderCodes() {
		group := ramp.ProviderGroup(code)
		settings.EXPECT().Bool(group, ramp.SettingEnabled).Return(enabled[code], nil).Once()
		settings.EXPECT().String(group, ramp.SettingMode).Return(models.ProviderModeSandbox, nil).Once()
	}
	return kyc.NewProviderKycService(kyc.Deps{
		Factory:  ramp.NewFactory(adapters...),
		Config:   ramp.NewConfig(settings),
		Features: features,
	})
}

// usaSwitch is the catalog on a memory store with the United States' switch
// set.
func usaSwitch(t *testing.T, on bool) contracts.Feature {
	t.Helper()
	m := featureflag.NewManager(drivers.NewMemory())
	catalog.Register(m)
	require.NoError(t, m.Set(context.Background(), kycflags.USAResidenceEnabled, nil, on))
	return m
}

// servesAnyone opens the payout to every identity everywhere, so what the
// catalog leaves out is its own doing.
func servesAnyone() jurisdictionAdapter {
	return jurisdictionAdapter{code: models.ProviderBridge, rails: func(ramp.TaxIdentity, string) []ramp.Rail {
		return []ramp.Rail{ramp.RailPixPayout}
	}}
}

func countryByCode(countries []dto.CountryDTO, code string) *dto.CountryDTO {
	for i := range countries {
		if countries[i].Code == code {
			return &countries[i]
		}
	}
	return nil
}

// cpfOption and passportOption are the two identities as the catalog lists
// them, served by the given providers.
func cpfOption(providers ...string) dto.TaxIdentityOptionDTO {
	return dto.TaxIdentityOptionDTO{
		Type:          models.TaxIDTypeCPF,
		Rails:         []string{models.RailPixPayin, models.RailPixPayout},
		DocumentTypes: []string{"national_id", "drivers_license", "passport"},
		Providers:     providers,
	}
}

func passportOption(providers ...string) dto.TaxIdentityOptionDTO {
	return dto.TaxIdentityOptionDTO{
		Type:          models.TaxIDTypePassport,
		Rails:         []string{models.RailPixPayout},
		DocumentTypes: []string{"passport"},
		Providers:     providers,
	}
}

func TestProviderKycService_Countries_BrazilOffersOnlyTheCPFWithBothRails(t *testing.T) {
	svc := newCountriesService(t, bridgeLike(), ozavLike())

	countries, err := svc.Countries(context.Background())

	require.NoError(t, err)
	bra := countryByCode(countries, "BRA")
	require.NotNil(t, bra, "Brazil is served")
	assert.Equal(t, dto.CountryDTO{
		Code: "BRA", Alpha2: "BR", FormProfile: kyc.FormProfileBR,
		PostalCodePattern: `^(?:\d{5}-?\d{3})$`,
		TaxIdentities:     []dto.TaxIdentityOptionDTO{cpfOption(models.ProviderBridge, models.ProviderOzav)},
	}, *bra)
}

func TestProviderKycService_Countries_AForeignResidenceOffersTheCPFAndItsOwnPassport(t *testing.T) {
	svc := newCountriesService(t, bridgeLike(), ozavLike())

	countries, err := svc.Countries(context.Background())

	require.NoError(t, err)
	mex := countryByCode(countries, "MEX")
	require.NotNil(t, mex, "Mexico is served")
	assert.Equal(t, dto.CountryDTO{
		Code: "MEX", Alpha2: "MX", FormProfile: kyc.FormProfileGeneric,
		PostalCodePattern: `^(?:\d{5})$`,
		TaxIdentities: []dto.TaxIdentityOptionDTO{
			cpfOption(models.ProviderBridge), passportOption(models.ProviderBridge),
		},
	}, *mex)
}

func TestProviderKycService_Countries_AResidenceNoProviderServesIsLeftOut(t *testing.T) {
	svc := newCountriesService(t, bridgeLike(), ozavLike())

	countries, err := svc.Countries(context.Background())

	require.NoError(t, err)
	assert.Nil(t, countryByCode(countries, "CUB"), "Cuba is refused by every provider")
	assert.Nil(t, countryByCode(countries, "USA"), "the United States is not served")
}

func TestProviderKycService_Countries_ACountryWithoutPostalCodesListsItsSubdivisions(t *testing.T) {
	svc := newCountriesService(t, bridgeLike(), ozavLike())

	countries, err := svc.Countries(context.Background())

	require.NoError(t, err)
	are := countryByCode(countries, "ARE")
	require.NotNil(t, are, "the Emirates are served")
	assert.Empty(t, are.PostalCodePattern, "the Emirates issue no postal code")
	assert.Equal(t, iso3166.RequiredSubdivisions("ARE"), are.Subdivisions)
	assert.Contains(t, are.Subdivisions, iso3166.Subdivision{Code: "DU", Name: "Dubai"})
}

func TestProviderKycService_Countries_OnlyOzavEnabledServesBrazilAlone(t *testing.T) {
	svc := newCountriesService(t, ozavLike())

	countries, err := svc.Countries(context.Background())

	require.NoError(t, err)
	require.Len(t, countries, 1)
	assert.Equal(t, "BRA", countries[0].Code)
	assert.Equal(t, []dto.TaxIdentityOptionDTO{cpfOption(models.ProviderOzav)}, countries[0].TaxIdentities)
}

func TestProviderKycService_Countries_AreSortedByCode(t *testing.T) {
	svc := newCountriesService(t, bridgeLike(), ozavLike())

	countries, err := svc.Countries(context.Background())

	require.NoError(t, err)
	assert.Greater(t, len(countries), 200, "every country but the refused ones")
	assert.True(t, sort.SliceIsSorted(countries, func(i, j int) bool { return countries[i].Code < countries[j].Code }))
}

// The rails of an identity are what any eligible provider opens, so two
// providers that each open one rail open both.
func TestProviderKycService_Countries_RailsAreTheUnionOverProviders(t *testing.T) {
	payinOnly := jurisdictionAdapter{code: models.ProviderBridge, rails: func(identity ramp.TaxIdentity, residence string) []ramp.Rail {
		if identity.Type == models.TaxIDTypeCPF && residence == "BRA" {
			return []ramp.Rail{ramp.RailPixPayin}
		}
		return nil
	}}
	payoutOnly := jurisdictionAdapter{code: models.ProviderOzav, rails: func(identity ramp.TaxIdentity, residence string) []ramp.Rail {
		if identity.Type == models.TaxIDTypeCPF && residence == "BRA" {
			return []ramp.Rail{ramp.RailPixPayout}
		}
		return nil
	}}
	svc := newCountriesService(t, payoutOnly, payinOnly)

	countries, err := svc.Countries(context.Background())

	require.NoError(t, err)
	require.Len(t, countries, 1)
	assert.Equal(t, []dto.TaxIdentityOptionDTO{cpfOption(models.ProviderBridge, models.ProviderOzav)}, countries[0].TaxIdentities)
}

func TestProviderKycService_Countries_AnIdentityNoProviderServesIsLeftOut(t *testing.T) {
	cpfOnly := jurisdictionAdapter{code: models.ProviderBridge, rails: func(identity ramp.TaxIdentity, residence string) []ramp.Rail {
		if identity.Type == models.TaxIDTypeCPF && residence == "MEX" {
			return []ramp.Rail{ramp.RailPixPayin, ramp.RailPixPayout}
		}
		return nil
	}}
	svc := newCountriesService(t, cpfOnly)

	countries, err := svc.Countries(context.Background())

	require.NoError(t, err)
	require.Len(t, countries, 1)
	assert.Equal(t, "MEX", countries[0].Code)
	assert.Equal(t, []dto.TaxIdentityOptionDTO{cpfOption(models.ProviderBridge)}, countries[0].TaxIdentities)
}

func TestProviderKycService_Countries_NoEligibleProviderServesNoCountry(t *testing.T) {
	svc := newCountriesService(t)

	countries, err := svc.Countries(context.Background())

	require.NoError(t, err)
	assert.NotNil(t, countries, "an empty list, not nil")
	assert.Empty(t, countries)
}

// An enabled stub serves nobody, whatever it would claim: the catalog narrows
// to implemented providers the way the fan-out does.
func TestProviderKycService_Countries_AnEnabledStubServesNobody(t *testing.T) {
	everyone := jurisdictionAdapter{code: models.ProviderMutualPay, rails: func(ramp.TaxIdentity, string) []ramp.Rail {
		return ramp.PixRails()
	}}
	svc := newCountriesService(t, stubJurisdictionAdapter{everyone})

	countries, err := svc.Countries(context.Background())

	require.NoError(t, err)
	assert.Empty(t, countries)
}

func TestProviderKycService_Countries_ASettingsFailureIsReturned(t *testing.T) {
	settings := mocks.NewMockSettingsSource(t)
	settings.EXPECT().Bool(ramp.ProviderGroup(models.ProviderBridge), ramp.SettingEnabled).
		Return(false, errors.New("redis down")).Once()
	svc := kyc.NewProviderKycService(kyc.Deps{
		Factory: ramp.NewFactory(bridgeLike()),
		Config:  ramp.NewConfig(settings),
	})

	countries, err := svc.Countries(context.Background())

	require.Error(t, err)
	assert.Nil(t, countries)
}

// The United States waits for its switch; a residence no switch holds is
// listed either way.
func TestProviderKycService_Countries_TheUnitedStatesWaitsForItsFlag(t *testing.T) {
	for _, on := range []bool{false, true} {
		svc := newCountriesServiceWith(t, usaSwitch(t, on), servesAnyone())

		countries, err := svc.Countries(context.Background())

		require.NoError(t, err)
		assert.Equal(t, on, countryByCode(countries, "USA") != nil, "flag on: %v", on)
		assert.NotNil(t, countryByCode(countries, "MEX"), "flag on: %v", on)
	}
}

// A service wired without flags keeps the United States shut.
func TestProviderKycService_Countries_AServiceWithoutFlagsKeepsTheUnitedStatesShut(t *testing.T) {
	svc := newCountriesService(t, servesAnyone())

	countries, err := svc.Countries(context.Background())

	require.NoError(t, err)
	assert.Nil(t, countryByCode(countries, "USA"))
	assert.NotNil(t, countryByCode(countries, "MEX"))
}

// A flag store that cannot answer leaves out only the residence it holds,
// logged; every other residence is listed and reads no flag (Once).
func TestProviderKycService_Countries_AFlagStoreFailureLeavesOnlyTheUnitedStatesOut(t *testing.T) {
	mock.Factory().Log()
	features := mocks.NewMockFeature(t)
	features.EXPECT().IsActive(testifymock.Anything, kycflags.USAResidenceEnabled, nil).
		Return(false, errors.New("store down")).Once()
	svc := newCountriesServiceWith(t, features, servesAnyone())

	countries, err := svc.Countries(context.Background())

	require.NoError(t, err)
	assert.Nil(t, countryByCode(countries, "USA"))
	assert.NotNil(t, countryByCode(countries, "BRA"))
	assert.NotNil(t, countryByCode(countries, "MEX"))
}

// usLike is Bridge's policy for the United States in miniature: the payout
// for an SSN or an ITIN issued by USA to a resident there, nothing else there.
func usLike() jurisdictionAdapter {
	return jurisdictionAdapter{code: models.ProviderBridge, rails: func(identity ramp.TaxIdentity, residence string) []ramp.Rail {
		if residence == "USA" && identity.IsUSTaxID() && identity.Country == "USA" {
			return []ramp.Rail{ramp.RailPixPayout}
		}
		return nil
	}}
}

// usOption is a US tax identity as the catalog lists it.
func usOption(taxIDType string) dto.TaxIdentityOptionDTO {
	return dto.TaxIdentityOptionDTO{
		Type:          taxIDType,
		Rails:         []string{models.RailPixPayout},
		DocumentTypes: []string{"drivers_license", "state_or_provincial_id", "passport"},
		Providers:     []string{models.ProviderBridge},
	}
}

// A resident of the United States declares an SSN or an ITIN and shows a US
// driver's license, a state ID or a passport — the license first, since the
// app preselects the first; the form names a state from Bridge's list.
func TestProviderKycService_Countries_TheUnitedStatesOffersAnSSNOrAnITIN(t *testing.T) {
	svc := newCountriesServiceWith(t, usaSwitch(t, true), usLike(), ozavLike())

	countries, err := svc.Countries(context.Background())

	require.NoError(t, err)
	usa := countryByCode(countries, "USA")
	require.NotNil(t, usa, "the United States is served")
	assert.Equal(t, dto.CountryDTO{
		Code: "USA", Alpha2: "US", FormProfile: kyc.FormProfileUS,
		PostalCodePattern: iso3166.PostalCodePattern("USA"),
		Subdivisions:      iso3166.RequiredSubdivisions("USA"),
		TaxIdentities:     []dto.TaxIdentityOptionDTO{usOption(models.TaxIDTypeSSN), usOption(models.TaxIDTypeITIN)},
	}, *usa)
	assert.Equal(t, "us", kyc.FormProfileUS)
}
