package ramp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

// fakeAdapter exists only to assert at compile time that a single struct can
// satisfy the whole contract, which is what the real provider packages do.
// Behavioural mocks come from mockery, never from here.
type fakeAdapter struct{}

func (fakeAdapter) Code() ProviderCode           { return ProviderBridge }
func (fakeAdapter) PreferredKycMode() KycMode    { return KycModeAPIDocs }
func (fakeAdapter) RequiresTos() bool            { return true }
func (fakeAdapter) Supports(cap Capability) bool { return cap.Rail == RailPixPayin }
func (fakeAdapter) CreateTosSession(context.Context, CustomerRef, TosOpts) (*TosSession, error) {
	return nil, ErrProviderNotImplemented
}
func (fakeAdapter) SubmitApplication(context.Context, CustomerRef, Application) (*KycState, error) {
	return nil, ErrProviderNotImplemented
}
func (fakeAdapter) SubmitDocuments(context.Context, CustomerRef, Documents) (*KycState, error) {
	return nil, ErrProviderNotImplemented
}
func (fakeAdapter) GetKyc(context.Context, CustomerRef) (*KycState, error) {
	return nil, ErrProviderNotImplemented
}
func (fakeAdapter) RefreshKyc(context.Context, CustomerRef) (*KycState, error) {
	return nil, ErrProviderNotImplemented
}
func (fakeAdapter) GetRequirements(context.Context, CustomerRef) (*KycRequirements, error) {
	return nil, ErrProviderNotImplemented
}
func (fakeAdapter) RequestEndorsements(context.Context, CustomerRef, []string) (*KycState, error) {
	return nil, ErrProviderNotImplemented
}
func (fakeAdapter) CreateHostedKycLink(context.Context, CustomerRef, HostedLinkOpts) (*HostedKycLink, error) {
	return nil, ErrProviderNotImplemented
}
func (fakeAdapter) EstimateOnramp(context.Context, CustomerRef, OnrampQuoteRequest) (*Estimate, error) {
	return nil, ErrProviderNotImplemented
}
func (fakeAdapter) CreateOnramp(context.Context, CustomerRef, OnrampCreateRequest) (*RampOrder, error) {
	return nil, ErrProviderNotImplemented
}
func (fakeAdapter) EstimateOfframp(context.Context, CustomerRef, OfframpQuoteRequest) (*Estimate, error) {
	return nil, ErrProviderNotImplemented
}
func (fakeAdapter) CreateOfframp(context.Context, CustomerRef, OfframpCreateRequest) (*RampOrder, error) {
	return nil, ErrProviderNotImplemented
}
func (fakeAdapter) GetOrder(context.Context, CustomerRef, string) (*RampOrder, error) {
	return nil, ErrProviderNotImplemented
}
func (fakeAdapter) ParseWebhook(context.Context, map[string]string, []byte) (*WebhookResult, error) {
	return nil, ErrProviderNotImplemented
}
func (fakeAdapter) DecodeWebhook(context.Context, []byte) (*WebhookResult, error) {
	return nil, ErrProviderNotImplemented
}
func (fakeAdapter) StartIntegration(context.Context, CustomerRef) (*Integration, error) {
	return nil, ErrProviderNotImplemented
}
func (fakeAdapter) RefreshIntegration(context.Context, CustomerRef) (*Integration, error) {
	return nil, ErrProviderNotImplemented
}

var (
	_ ProviderAdapter    = (*fakeAdapter)(nil)
	_ KycAdapter         = (*fakeAdapter)(nil)
	_ RampAdapter        = (*fakeAdapter)(nil)
	_ IntegrationAdapter = (*fakeAdapter)(nil)
)

// fakeVirtualAccountAdapter is a second, separate struct on purpose: it pins
// that VirtualAccountAdapter is satisfiable without ProviderAdapter. Declaring
// the methods on fakeAdapter would have compiled either way and hidden the day
// someone folds the port back into the fat interface.
type fakeVirtualAccountAdapter struct{}

func (fakeVirtualAccountAdapter) Code() ProviderCode { return ProviderBridge }
func (fakeVirtualAccountAdapter) ProvisionVirtualAccount(context.Context, CustomerRef, VirtualAccountRequest) (*VirtualAccount, error) {
	return nil, ErrProviderNotImplemented
}
func (fakeVirtualAccountAdapter) GetVirtualAccount(context.Context, CustomerRef, string) (*VirtualAccount, error) {
	return nil, ErrProviderNotImplemented
}
func (fakeVirtualAccountAdapter) UpdateVirtualAccount(context.Context, CustomerRef, string, VirtualAccountUpdate) (*VirtualAccount, error) {
	return nil, ErrProviderNotImplemented
}
func (fakeVirtualAccountAdapter) GetDeposit(context.Context, CustomerRef, string, string) (*VirtualAccountDeposit, error) {
	return nil, ErrProviderNotImplemented
}

var _ VirtualAccountAdapter = (*fakeVirtualAccountAdapter)(nil)

// An address has to survive the round trip through the contract with every
// component a Brazilian address actually has. Bairro and número are not
// decoration: a provider that asks for them separately has nowhere to read
// them from if the contract folds them into the street line.
func TestAddressCarriesNeighborhoodAndNumber(t *testing.T) {
	addr := Address{
		StreetLine1:  "Avenida Paulista",
		Number:       "1000",
		Neighborhood: "Bela Vista",
		City:         "São Paulo",
		Subdivision:  "SP",
		PostalCode:   "01310000",
		Country:      "BRA",
	}

	assert.Equal(t, "1000", addr.Number)
	assert.Equal(t, "Bela Vista", addr.Neighborhood)
	// The zero value must stay comparable: bridge/kyc_adapter.go relies on
	// `a == (ramp.Address{})` to decide whether to send an address at all.
	// That comparison only stays safe because street_line_1 and city are
	// `required` on SubmitApplicationRequest — Number and Neighborhood being
	// optional cannot by itself produce a real submission that is all-empty.
	// Making street_line_1 or city optional too would let that happen, and
	// addressFrom would start sending Bridge a payload with blank required
	// fields instead of skipping the address.
	assert.NotEqual(t, Address{}, addr)
}

// TestRailValuesMatchModels guards the one thing that silently breaks routing:
// the typed rails here and the strings persisted in `rail_routing_settings`
// must be the same values.
func TestRailValuesMatchModels(t *testing.T) {
	assert.Equal(t, "pix_payin", RailPixPayin.String())
	assert.Equal(t, "pix_payout", RailPixPayout.String())
}

// TestProviderCodesAreDistinct catches a copy-paste in the provider constants,
// which would make the factory hand back the wrong adapter.
func TestProviderCodesAreDistinct(t *testing.T) {
	codes := map[ProviderCode]bool{}
	for _, code := range []ProviderCode{ProviderBridge, ProviderMutualPay, ProviderOzav} {
		assert.NotEmpty(t, code)
		assert.False(t, codes[code], "duplicate provider code %q", code)
		codes[code] = true
	}
}

// TestProfileCarriesTheRiskProfileFields pins the contract: the fields Ozav
// lists in requirements.missing_fields have a home on Profile, and the two
// numbers plus the PEP flag are pointers so "not supplied" and "supplied as
// zero/false" stay distinct — Ozav treats 0 income as an answer and reads an
// omitted is_pep as false, and only the client may make either statement.
func TestProfileCarriesTheRiskProfileFields(t *testing.T) {
	income, volume, pep := 500.0, 5000.0, false
	p := Profile{
		Nationality:              "BRA",
		Occupation:               "Software developer",
		SourceOfFunds:            "employment_salary",
		AccountPurpose:           "receive_payments",
		MonthlyIncomeUSD:         &income,
		ExpectedMonthlyVolumeUSD: &volume,
		IsPEP:                    &pep,
	}
	assert.Equal(t, "BRA", p.Nationality)
	assert.Equal(t, 500.0, *p.MonthlyIncomeUSD)
	assert.False(t, *p.IsPEP)
	assert.Nil(t, Profile{}.IsPEP, "an app that never asked must not declare anything")
}

func TestDocumentsCarryAProofOfAddress(t *testing.T) {
	d := Documents{ProofOfAddress: "data:image/jpeg;base64,AAAA"}
	assert.Equal(t, "data:image/jpeg;base64,AAAA", d.ProofOfAddress)
}

// atomicCompileCheck proves the AtomicKycCreator shape is stable.
type atomicCompileCheck struct{}

func (atomicCompileCheck) ValidateDeferredApplication(Application) (*KycState, error) {
	return nil, nil
}
func (atomicCompileCheck) ValidateAtomicDocuments(Documents) error { return nil }
func (atomicCompileCheck) PrepareCustomer(context.Context, AtomicKycSubmission, AtomicKycCheckpoint,
	func(AtomicKycCheckpoint) error) (*PreparedAtomicKyc, error) {
	return nil, nil
}
func (atomicCompileCheck) CreatePreparedCustomer(context.Context, string, PreparedAtomicKyc) (*KycState, error) {
	return nil, nil
}

var _ AtomicKycCreator = (*atomicCompileCheck)(nil)
