package kyc_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/kyc"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
)

// servesAnyCompany takes a company of any country, opening it the deposit.
func servesAnyCompany() businessBridgeLike {
	return businessBridgeLike{jurisdictionAdapter{code: models.ProviderBridge, rails: func(identity ramp.TaxIdentity, _ string) []ramp.Rail {
		if !identity.IsBusiness() {
			return nil
		}
		return []ramp.Rail{ramp.RailPixPayin}
	}}}
}

// A company is served where some provider taking companies opens it a rail,
// read the way the countries catalog lists it (spec §7.2): a CNPJ or a UK CRN
// at Bridge; nothing in the EEA, nothing where only Ozav is enabled.
func TestRefuseUnservedBusiness(t *testing.T) {
	for _, tc := range []struct {
		name     string
		identity models.TaxIdentity
		adapters []ramp.ProviderAdapter
		served   bool
	}{
		{"a cnpj at bridge", models.TaxIdentity{Type: models.TaxIDTypeCNPJ, Country: "BRA"},
			[]ramp.ProviderAdapter{newBusinessBridgeLike(), ozavLike()}, true},
		{"a uk crn at bridge", models.TaxIdentity{Type: models.TaxIDTypeCRN, Country: "GBR"},
			[]ramp.ProviderAdapter{newBusinessBridgeLike()}, true},
		{"a company in the eea", models.TaxIdentity{Type: models.TaxIDTypeVAT, Country: "PRT"},
			[]ramp.ProviderAdapter{newBusinessBridgeLike()}, false},
		{"a cnpj where no provider takes companies", models.TaxIdentity{Type: models.TaxIDTypeCNPJ, Country: "BRA"},
			[]ramp.ProviderAdapter{ozavLike()}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := newCountriesService(t, tc.adapters...).RefuseUnservedBusiness(context.Background(), tc.identity)
			if tc.served {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, kyc.ErrJurisdictionNotServed)
		})
	}
}

// A company is no resident (PLANO B2): the flag that holds the United States
// shut to the people living there (kycflags.ResidenceFlag) holds none of its
// companies, and is not even read for one — a store that cannot answer, or
// none wired, refuses nothing. What Bridge refuses stays refused.
func TestRefuseUnservedBusinessReadsNoResidenceFlag(t *testing.T) {
	ein := models.TaxIdentity{Type: models.TaxIDTypeEIN, Country: "USA"}
	for name, svc := range map[string]*kyc.ProviderKycService{
		"the flag off":              newCountriesServiceWith(t, usaSwitch(t, false), servesAnyCompany()),
		"no flags wired":            newCountriesService(t, servesAnyCompany()),
		"a store that is not asked": newCountriesServiceWith(t, mocks.NewMockFeature(t), servesAnyCompany()),
	} {
		t.Run(name, func(t *testing.T) {
			require.NoError(t, svc.RefuseUnservedBusiness(context.Background(), ein))
		})
	}

	err := newCountriesServiceWith(t, usaSwitch(t, true), newBusinessBridgeLike()).
		RefuseUnservedBusiness(context.Background(), models.TaxIdentity{Type: models.TaxIDTypeVAT, Country: "PRT"})
	require.ErrorIs(t, err, kyc.ErrJurisdictionNotServed, "the EEA stays closed to companies")
}

// What GET /api/customers summarises: the rows the customer holds, marked
// like GET /api/kyc marks them, and no row made up for a provider it never
// touched.
func TestCustomerKycListsOnlyTheStoredRows(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge, models.ProviderOzav)
	h.allowTos()
	h.kycRepo.EXPECT().ListByCustomer(uint(7)).Return([]models.CustomerProviderKyc{
		providerState(models.ProviderBridge, models.KycStatusApproved),
	}, nil).Once()

	rows, err := h.svc.CustomerKyc(7)

	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, models.ProviderBridge, rows[0].Provider)
	assert.Equal(t, models.KycStatusApproved, rows[0].KycStatus)
}

func TestCustomerKycOfACustomerThatNeverStartedIsEmpty(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge)
	h.allowTos()
	h.kycRepo.EXPECT().ListByCustomer(uint(7)).Return(nil, nil).Once()

	rows, err := h.svc.CustomerKyc(7)

	require.NoError(t, err)
	assert.Empty(t, rows)
}

// Registering a wallet waits for the approval the app reads in the overview:
// approved anywhere passes.
func TestRequireApprovedPassesAnApprovedCustomer(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge)
	h.allowTos()
	h.kycRepo.EXPECT().ListByCustomer(uint(7)).Return([]models.CustomerProviderKyc{
		providerState(models.ProviderBridge, models.KycStatusApproved),
	}, nil).Once()
	h.integRepo.EXPECT().ListByCustomer(uint(7)).Return([]models.CustomerProviderIntegration{
		linkFor(models.ProviderBridge, "cus_1"),
	}, nil).Once()
	h.inFlight(models.ProviderBridge, "")

	require.NoError(t, h.svc.RequireApproved(7))
}

// Short of an approval it is kyc_pending, naming the one provider that serves
// the customer and the steps the overview opens, so the app sends the user to
// them; every provider's final refusal is kyc_rejected.
func TestRequireApprovedRefusesWithWhatTheOverviewSays(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge)
	h.allowTos()
	h.kycRepo.EXPECT().ListByCustomer(uint(7)).Return(nil, nil).Once()
	h.integRepo.EXPECT().ListByCustomer(uint(7)).Return(nil, nil).Once()
	h.inFlight(models.ProviderBridge, "")

	err := h.svc.RequireApproved(7)

	var blocked *ramp.KycBlocked
	require.True(t, errors.As(err, &blocked), "%v", err)
	assert.ErrorIs(t, err, ramp.ErrKycPending)
	assert.Equal(t, models.ProviderBridge, blocked.Provider)
	assert.Equal(t, []string{"personal", "profile", "address", "document", "selfie"}, blocked.PendingSteps)
}

func TestRequireApprovedRefusesAFinalRejection(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge)
	h.allowTos()
	rejected := providerState(models.ProviderBridge, models.KycStatusRejected)
	rejected.RejectionCategories = `["not_eligible"]`
	h.kycRepo.EXPECT().ListByCustomer(uint(7)).Return([]models.CustomerProviderKyc{rejected}, nil).Once()
	h.integRepo.EXPECT().ListByCustomer(uint(7)).Return([]models.CustomerProviderIntegration{
		linkFor(models.ProviderBridge, "cus_1"),
	}, nil).Once()
	h.inFlight(models.ProviderBridge, "")

	err := h.svc.RequireApproved(7)

	assert.ErrorIs(t, err, ramp.ErrKycRejected)
	var blocked *ramp.KycBlocked
	require.True(t, errors.As(err, &blocked))
	assert.Empty(t, blocked.PendingSteps)
}

// Two providers serving the customer name none: the app opens the overview.
func TestRequireApprovedNamesNoProviderWhenSeveralServe(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge, models.ProviderOzav)
	h.allowTos()
	h.kycRepo.EXPECT().ListByCustomer(uint(7)).Return(nil, nil).Once()
	h.integRepo.EXPECT().ListByCustomer(uint(7)).Return(nil, nil).Once()
	h.inFlight(models.ProviderBridge, "")
	h.inFlight(models.ProviderOzav, "")

	var blocked *ramp.KycBlocked
	require.True(t, errors.As(h.svc.RequireApproved(7), &blocked))
	assert.Empty(t, blocked.Provider)
}
