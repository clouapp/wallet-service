// The router tests live in the external test package because they use the
// generated mocks, and app/mocks imports ramp — an in-package test would be an
// import cycle.
package ramp_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
	"github.com/macrowallets/waas/packages/rampengine/tests/support"
)

const testEnv = "testing"

// routerStub is shaped like the shipped MutualPay/Ozav stubs: it satisfies the
// contract and carries the IsStub marker, so the router must skip it. Every
// operation refusing is what proves it was never called.
type routerStub struct {
	ramp.NotImplemented
	code ramp.ProviderCode
}

func (s routerStub) Code() ramp.ProviderCode      { return s.code }
func (routerStub) PreferredKycMode() ramp.KycMode { return ramp.KycModeHostedVerification }
func (routerStub) RequiresTos() bool              { return false }
func (routerStub) Supports(ramp.Capability) bool  { return true }

type harness struct {
	settings  *mocks.MockSettingsSource
	integ     *mocks.MockCustomerProviderIntegrationRepository
	kycs      *mocks.MockCustomerProviderKycRepository
	profiles  *mocks.MockCustomerIndividualProfileRepository
	customers *mocks.MockCustomerRepository
	adapters  []ramp.ProviderAdapter
	// retained is the profile the user retained; nil, none — every test
	// before eligibility was per person reads that way.
	retained *ramp.Profile
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	return &harness{
		settings:  mocks.NewMockSettingsSource(t),
		integ:     mocks.NewMockCustomerProviderIntegrationRepository(t),
		kycs:      mocks.NewMockCustomerProviderKycRepository(t),
		profiles:  mocks.NewMockCustomerIndividualProfileRepository(t),
		customers: support.IndividualCustomers(t),
	}
}

// router answers the profile read with h.retained, unless the test set its
// own expectation on it — the one that counts the reads.
func (h *harness) router() *ramp.Router {
	if len(h.profiles.ExpectedCalls) == 0 {
		h.profiles.EXPECT().Find(testifymock.Anything).
			RunAndReturn(func(uint) (*ramp.Profile, error) { return h.retained, nil }).Maybe()
	}
	return ramp.NewRouter(ramp.RouterDeps{
		Factory: ramp.NewFactory(h.adapters...), Config: ramp.NewConfig(h.settings),
		Integrations: h.integ, Kyc: h.kycs, Persons: ramp.Persons{Customers: h.customers, Profiles: h.profiles}, AppEnv: testEnv,
	})
}

// servesEverything is the default capability answer. Only the tests about the
// token filter override it.
func servesEverything(ramp.Capability) bool { return true }

// servesToken answers for a provider whose vocabulary is exactly one token. An
// unnamed token is a question the caller did not ask, so it is accepted — the
// sticky create path resolves on the rail alone.
func servesToken(token string) func(ramp.Capability) bool {
	return func(cap ramp.Capability) bool {
		return cap.Token == "" || strings.EqualFold(cap.Token, token)
	}
}

// realAdapter registers an implemented provider. Nothing is stubbed on it
// beyond Code and Supports, so any unexpected call fails the test.
func (h *harness) realAdapter(t *testing.T, code ramp.ProviderCode) *mocks.MockProviderAdapter {
	return h.servingAdapter(t, code, servesEverything)
}

// servingAdapter is realAdapter with a capability answer of the test's own.
//
// Supports is registered exactly once, with RunAndReturn rather than a second
// Return: testify answers from the FIRST matching expectation, so a broad
// Return(true) registered here would swallow every narrower one a test adds.
func (h *harness) servingAdapter(
	t *testing.T, code ramp.ProviderCode, serves func(ramp.Capability) bool,
) *mocks.MockProviderAdapter {
	t.Helper()
	adapter := mocks.NewMockProviderAdapter(t)
	adapter.EXPECT().Code().Return(code).Maybe()
	adapter.EXPECT().Supports(testifymock.Anything).RunAndReturn(serves).Maybe()
	h.adapters = append(h.adapters, adapter)
	return adapter
}

func (h *harness) order(rail string, codes ...string) {
	group := ramp.RailGroup(ramp.Rail(rail))
	h.settings.EXPECT().String(group, ramp.SettingProviderOrder).
		Return(ramp.FormatProviderOrder(codes), nil)
	h.settings.EXPECT().String(group, ramp.FeeSettingKey(ramp.ProviderBridge)).Return("1.00", nil)
	h.settings.EXPECT().String(group, ramp.FeeSettingKey(ramp.ProviderOzav)).Return("1.00", nil)
	h.settings.EXPECT().String(group, ramp.FeeSettingKey(ramp.ProviderBlindPay)).Return("0.00", nil).Maybe()
	h.settings.EXPECT().String(group, ramp.SettingMinAmountBRL).Return("", nil)
	h.settings.EXPECT().String(group, ramp.SettingMaxAmountBRL).Return("", nil)
	h.settings.EXPECT().String(group, ramp.SettingMinAmountBRLByToken).Return("", nil)
	switch ramp.Rail(rail) {
	case ramp.RailPixPayin:
		h.settings.EXPECT().String(group, ramp.SettingOzavDeliveryMaxAttempts).Return("3", nil).Maybe()
	case ramp.RailPixPayout:
		h.settings.EXPECT().String(group, ramp.SettingOzavOfframpFundingWindowMinutes).Return("30", nil).Maybe()
		h.settings.EXPECT().String(group, ramp.SettingOzavDepositMatchTolerance).Return("0", nil).Maybe()
	}
}

func (h *harness) config(code string, enabled bool, mode string) {
	group := ramp.ProviderGroup(code)
	h.settings.EXPECT().Bool(group, ramp.SettingEnabled).Return(enabled, nil).Maybe()
	h.settings.EXPECT().String(group, ramp.SettingMode).Return(mode, nil).Maybe()
}

func (h *harness) integration(userID uint, code string, customerID *string, status string) {
	h.integ.EXPECT().FindByCustomerAndProvider(userID, code).Return(&models.CustomerProviderIntegration{
		CustomerID: userID, Provider: code, ExternalCustomerID: customerID, Status: status,
	}, nil).Maybe()
}

func (h *harness) noIntegration(userID uint, code string) {
	h.integ.EXPECT().FindByCustomerAndProvider(userID, code).Return(nil, nil).Maybe()
}

func (h *harness) kyc(userID uint, code, endorsements string) {
	h.kycWithStatus(userID, code, models.KycStatusApproved, endorsements)
}

// kycWithStatus is the same row with the compliance status spelled out. It is
// what separates a provider the user can still finish from one that refused.
func (h *harness) kycWithStatus(userID uint, code, status, endorsements string) {
	h.kycs.EXPECT().FindByCustomerAndProvider(userID, code).Return(&models.CustomerProviderKyc{
		CustomerID: userID, Provider: code, KycStatus: status, Endorsements: endorsements,
	}, nil).Maybe()
}

func (h *harness) noKyc(userID uint, code string) {
	h.kycs.EXPECT().FindByCustomerAndProvider(userID, code).Return(nil, nil).Maybe()
}

const approvedPix = `[{"name":"pix","status":"approved"}]`

// eligible wires a provider all the way through the chain.
func (h *harness) eligible(t *testing.T, userID uint, code string) *mocks.MockProviderAdapter {
	return h.eligibleServing(t, userID, code, servesEverything)
}

// eligibleServing is eligible for a provider whose vocabulary is narrower than
// "everything" — an approved provider that simply does not serve the token.
func (h *harness) eligibleServing(
	t *testing.T, userID uint, code string, serves func(ramp.Capability) bool,
) *mocks.MockProviderAdapter {
	t.Helper()
	adapter := h.servingAdapter(t, code, serves)
	h.config(code, true, models.ProviderModeSandbox)
	h.integration(userID, code, ptr("cus_"+code), models.IntegrationStatusActive)
	h.kyc(userID, code, approvedPix)
	return adapter
}

// blockedByKyc wires a provider that is usable and linked but whose
// endorsements do not cover the rail, with the compliance status the test needs.
// serves is its capability answer: a provider can be both blocked on KYC and
// unable to price the token, and telling those two apart is the whole point of
// where the Supports guard sits.
func (h *harness) blockedByKyc(
	t *testing.T, userID uint, code, status string, serves func(ramp.Capability) bool,
) *mocks.MockProviderAdapter {
	t.Helper()
	adapter := h.servingAdapter(t, code, serves)
	h.config(code, true, models.ProviderModeSandbox)
	h.integration(userID, code, ptr("cus_"+code), models.IntegrationStatusActive)
	h.kycWithStatus(userID, code, status, "[]")
	return adapter
}

func ptr[T any](v T) *T { return &v }

func TestResolveRespectsConfiguredOrder(t *testing.T) {
	h := newHarness(t)
	const userID = uint(1)
	h.eligible(t, userID, models.ProviderOzav)
	h.eligible(t, userID, models.ProviderBridge)
	// Ozav first: the admin's order is the policy, not the registration order.
	h.order(models.RailPixPayin, models.ProviderOzav, models.ProviderBridge)

	adapter, ref, err := h.router().Resolve(context.Background(), userID, ramp.Capability{Rail: ramp.RailPixPayin})
	require.NoError(t, err)
	require.Equal(t, models.ProviderOzav, adapter.Code())
	require.Equal(t, "cus_"+models.ProviderOzav, ref.ExternalID)
	require.Equal(t, userID, ref.CustomerID)
}

func TestResolveSkipsDisabledConfig(t *testing.T) {
	h := newHarness(t)
	const userID = uint(1)
	h.realAdapter(t, models.ProviderOzav)
	h.config(models.ProviderOzav, false, models.ProviderModeSandbox)
	h.eligible(t, userID, models.ProviderBridge)
	h.order(models.RailPixPayin, models.ProviderOzav, models.ProviderBridge)

	adapter, _, err := h.router().Resolve(context.Background(), userID, ramp.Capability{Rail: ramp.RailPixPayin})
	require.NoError(t, err)
	require.Equal(t, models.ProviderBridge, adapter.Code())
}

// A provider whose mode disagrees with APP_ENV must never be routed to, even
// when it is enabled and the user is fully cleared for it.
func TestResolveSkipsModeMismatch(t *testing.T) {
	h := newHarness(t)
	const userID = uint(1)
	h.realAdapter(t, models.ProviderOzav)
	h.config(models.ProviderOzav, true, models.ProviderModeProduction)
	h.integration(userID, models.ProviderOzav, ptr("cus_ozav"), models.IntegrationStatusActive)
	h.kyc(userID, models.ProviderOzav, approvedPix)
	h.eligible(t, userID, models.ProviderBridge)
	h.order(models.RailPixPayin, models.ProviderOzav, models.ProviderBridge)

	adapter, _, err := h.router().Resolve(context.Background(), userID, ramp.Capability{Rail: ramp.RailPixPayin})
	require.NoError(t, err)
	require.Equal(t, models.ProviderBridge, adapter.Code())
}

// The stub has no expectations set on it: mockery fails the test if anything
// calls it, which is exactly the guarantee wanted here.
func TestResolveSkipsStubWithoutCallingIt(t *testing.T) {
	h := newHarness(t)
	const userID = uint(1)
	h.adapters = append(h.adapters, routerStub{code: models.ProviderOzav})
	h.config(models.ProviderOzav, true, models.ProviderModeSandbox)
	h.eligible(t, userID, models.ProviderBridge)
	h.order(models.RailPixPayin, models.ProviderOzav, models.ProviderBridge)

	adapter, _, err := h.router().Resolve(context.Background(), userID, ramp.Capability{Rail: ramp.RailPixPayin})
	require.NoError(t, err)
	require.Equal(t, models.ProviderBridge, adapter.Code())
}

func TestResolveSkipsMissingIntegration(t *testing.T) {
	h := newHarness(t)
	const userID = uint(1)
	h.realAdapter(t, models.ProviderOzav)
	h.config(models.ProviderOzav, true, models.ProviderModeSandbox)
	h.noIntegration(userID, models.ProviderOzav)
	h.eligible(t, userID, models.ProviderBridge)
	h.order(models.RailPixPayin, models.ProviderOzav, models.ProviderBridge)

	adapter, _, err := h.router().Resolve(context.Background(), userID, ramp.Capability{Rail: ramp.RailPixPayin})
	require.NoError(t, err)
	require.Equal(t, models.ProviderBridge, adapter.Code())
}

// An integration with no customer id is not routable: every adapter call needs
// that id, so it would fail at the provider.
func TestResolveSkipsIntegrationWithoutCustomerID(t *testing.T) {
	h := newHarness(t)
	const userID = uint(1)
	h.realAdapter(t, models.ProviderBridge)
	h.config(models.ProviderBridge, true, models.ProviderModeSandbox)
	h.integration(userID, models.ProviderBridge, nil, models.IntegrationStatusActive)
	h.order(models.RailPixPayin, models.ProviderBridge)

	_, _, err := h.router().Resolve(context.Background(), userID, ramp.Capability{Rail: ramp.RailPixPayin})
	require.ErrorIs(t, err, ramp.ErrIntegrationRequired)
}

func TestResolveSkipsRailNotCoveredByEndorsements(t *testing.T) {
	h := newHarness(t)
	const userID = uint(1)
	h.realAdapter(t, models.ProviderBridge)
	h.config(models.ProviderBridge, true, models.ProviderModeSandbox)
	h.integration(userID, models.ProviderBridge, ptr("cus_bridge"), models.IntegrationStatusActive)
	// Cleared for on-ramp only.
	h.kyc(userID, models.ProviderBridge, `[{"name":"pix_onramp","status":"approved"}]`)
	h.order(models.RailPixPayout, models.ProviderBridge)

	_, _, err := h.router().Resolve(context.Background(), userID, ramp.Capability{Rail: ramp.RailPixPayout})
	require.ErrorIs(t, err, ramp.ErrKycPending)
}

func TestResolveClassifiesEmptyResults(t *testing.T) {
	const userID = uint(1)

	// An empty order is the shape a rail closed for business takes now that
	// every rail always resolves to an effective value. It must still fail
	// closed rather than route somewhere unintended.
	t.Run("provider order is empty", func(t *testing.T) {
		h := newHarness(t)
		h.order(models.RailPixPayin)

		_, _, err := h.router().Resolve(context.Background(), userID, ramp.Capability{Rail: ramp.RailPixPayin})
		require.ErrorIs(t, err, ramp.ErrNoProviderAvailable)
	})

	t.Run("only stubs configured", func(t *testing.T) {
		h := newHarness(t)
		h.adapters = append(h.adapters, routerStub{code: models.ProviderOzav})
		h.config(models.ProviderOzav, true, models.ProviderModeSandbox)
		h.order(models.RailPixPayin, models.ProviderOzav)

		_, _, err := h.router().Resolve(context.Background(), userID, ramp.Capability{Rail: ramp.RailPixPayin})
		require.ErrorIs(t, err, ramp.ErrNoProviderAvailable)
	})

	t.Run("user has not linked any provider", func(t *testing.T) {
		h := newHarness(t)
		h.realAdapter(t, models.ProviderBridge)
		h.config(models.ProviderBridge, true, models.ProviderModeSandbox)
		h.noIntegration(userID, models.ProviderBridge)
		h.order(models.RailPixPayin, models.ProviderBridge)

		_, _, err := h.router().Resolve(context.Background(), userID, ramp.Capability{Rail: ramp.RailPixPayin})
		require.ErrorIs(t, err, ramp.ErrIntegrationRequired)
	})

	// The furthest rejection wins: telling a user to link an account they
	// already linked, because some other provider was never configured, sends
	// them down the wrong path.
	t.Run("kyc pending outranks an unconfigured provider", func(t *testing.T) {
		h := newHarness(t)
		h.realAdapter(t, models.ProviderOzav)
		h.config(models.ProviderOzav, false, models.ProviderModeSandbox)
		h.realAdapter(t, models.ProviderBridge)
		h.config(models.ProviderBridge, true, models.ProviderModeSandbox)
		h.integration(userID, models.ProviderBridge, ptr("cus_bridge"), models.IntegrationStatusActive)
		h.noKyc(userID, models.ProviderBridge)
		h.order(models.RailPixPayin, models.ProviderOzav, models.ProviderBridge)

		_, _, err := h.router().Resolve(context.Background(), userID, ramp.Capability{Rail: ramp.RailPixPayin})
		require.ErrorIs(t, err, ramp.ErrKycPending)
	})

	t.Run("kyc pending outranks a missing integration", func(t *testing.T) {
		h := newHarness(t)
		h.realAdapter(t, models.ProviderOzav)
		h.config(models.ProviderOzav, true, models.ProviderModeSandbox)
		h.noIntegration(userID, models.ProviderOzav)
		h.realAdapter(t, models.ProviderBridge)
		h.config(models.ProviderBridge, true, models.ProviderModeSandbox)
		h.integration(userID, models.ProviderBridge, ptr("cus_bridge"), models.IntegrationStatusActive)
		h.noKyc(userID, models.ProviderBridge)
		h.order(models.RailPixPayin, models.ProviderOzav, models.ProviderBridge)

		_, _, err := h.router().Resolve(context.Background(), userID, ramp.Capability{Rail: ramp.RailPixPayin})
		require.ErrorIs(t, err, ramp.ErrKycPending)
	})
}

func TestResolveProviderIsSticky(t *testing.T) {
	h := newHarness(t)
	const userID = uint(1)
	h.eligible(t, userID, models.ProviderBridge)

	adapter, ref, err := h.router().ResolveProvider(context.Background(), userID, ramp.RailPixPayin, models.ProviderBridge)
	require.NoError(t, err)
	require.Equal(t, models.ProviderBridge, adapter.Code())
	require.Equal(t, "cus_bridge", ref.ExternalID)
}

// The sticky path applies the same eligibility: a provider disabled between
// estimate and create must not still take the order.
func TestResolveProviderRefusesIneligible(t *testing.T) {
	h := newHarness(t)
	const userID = uint(1)
	h.realAdapter(t, models.ProviderBridge)
	h.config(models.ProviderBridge, false, models.ProviderModeSandbox)

	_, _, err := h.router().ResolveProvider(context.Background(), userID, ramp.RailPixPayin, models.ProviderBridge)
	require.ErrorIs(t, err, ramp.ErrNoProviderAvailable)
}

// An estimate moves no money, so it may fail over on any error.
func TestResolveWithCallEstimatePolicyAdvancesOnAnyError(t *testing.T) {
	h := newHarness(t)
	const userID = uint(1)
	h.eligible(t, userID, models.ProviderOzav)
	h.eligible(t, userID, models.ProviderBridge)
	h.order(models.RailPixPayin, models.ProviderOzav, models.ProviderBridge)

	var tried []ramp.ProviderCode
	code, err := h.router().ResolveWithCall(context.Background(), userID, ramp.Capability{Rail: ramp.RailPixPayin},
		ramp.FailoverOnAnyError,
		func(_ context.Context, adapter ramp.ProviderAdapter, _ ramp.CustomerRef) error {
			tried = append(tried, adapter.Code())
			if adapter.Code() == models.ProviderOzav {
				return errors.New("upstream 503")
			}
			return nil
		})

	require.NoError(t, err)
	require.Equal(t, models.ProviderBridge, code)
	require.Len(t, tried, 2)
}

// A provider that does not collect through a standing account was never asked
// to do anything, so the chain moves past it. This is the case Resolve cannot
// serve at all: it returns candidates[0] and would report the rail unserved
// while the provider behind the head sat ready.
func TestResolveWithCallStandingAccountPolicySkipsAProviderWithoutThePort(t *testing.T) {
	h := newHarness(t)
	const userID = uint(1)
	h.eligible(t, userID, models.ProviderOzav)
	h.eligible(t, userID, models.ProviderBridge)
	h.order(models.RailPixPayin, models.ProviderOzav, models.ProviderBridge)

	var tried []ramp.ProviderCode
	code, err := h.router().ResolveWithCall(context.Background(), userID, ramp.Capability{Rail: ramp.RailPixPayin},
		ramp.FailoverOnUnsupported,
		func(_ context.Context, adapter ramp.ProviderAdapter, _ ramp.CustomerRef) error {
			tried = append(tried, adapter.Code())
			if adapter.Code() == models.ProviderOzav {
				return fmt.Errorf("provider %q: %w", adapter.Code(), ramp.ErrStandingAccountUnsupported)
			}
			return nil
		})

	require.NoError(t, err)
	require.Equal(t, models.ProviderBridge, code)
	require.Equal(t, []ramp.ProviderCode{models.ProviderOzav, models.ProviderBridge}, tried)
}

// The rule that keeps one user from holding two deposit codes: a provisioning
// call that failed may still have created the account upstream, so no other
// provider is tried.
func TestResolveWithCallStandingAccountPolicyStopsOnAFailedProvision(t *testing.T) {
	h := newHarness(t)
	const userID = uint(1)
	h.eligible(t, userID, models.ProviderOzav)
	h.eligible(t, userID, models.ProviderBridge)
	h.order(models.RailPixPayin, models.ProviderOzav, models.ProviderBridge)

	upstream := errors.New("bridge: POST /virtual_accounts: 500")
	var tried []ramp.ProviderCode
	_, err := h.router().ResolveWithCall(context.Background(), userID, ramp.Capability{Rail: ramp.RailPixPayin},
		ramp.FailoverOnUnsupported,
		func(_ context.Context, adapter ramp.ProviderAdapter, _ ramp.CustomerRef) error {
			tried = append(tried, adapter.Code())
			return upstream
		})

	require.ErrorIs(t, err, upstream)
	require.Equal(t, []ramp.ProviderCode{models.ProviderOzav}, tried,
		"the provider may already have created the account; a second attempt is a second deposit code")
}

func TestResolveWithCallExhaustsTheChain(t *testing.T) {
	h := newHarness(t)
	const userID = uint(1)
	h.eligible(t, userID, models.ProviderBridge)
	h.order(models.RailPixPayin, models.ProviderBridge)

	boom := errors.New("upstream 503")
	_, err := h.router().ResolveWithCall(context.Background(), userID, ramp.Capability{Rail: ramp.RailPixPayin},
		ramp.FailoverOnAnyError,
		func(context.Context, ramp.ProviderAdapter, ramp.CustomerRef) error { return boom })

	require.ErrorIs(t, err, boom)
}

// A settlement step must not be gated on eligibility.
//
// Eligibility answers "may this user start something". Finishing a movement the
// user already paid for is a different question, and asking the first one
// freezes exactly the rows that need resolving: a customer suspended after
// paying, or a provider an operator disabled during an incident, would leave
// the crypto in the provider's custody with every retry refusing identically.
func TestResolveForSettlementIgnoresEligibility(t *testing.T) {
	adapter := mocks.NewMockProviderAdapter(t)
	adapter.EXPECT().Code().Return(models.ProviderOzav).Maybe()

	settings := mocks.NewMockSettingsSource(t)
	integ := mocks.NewMockCustomerProviderIntegrationRepository(t)
	kycs := mocks.NewMockCustomerProviderKycRepository(t)

	integ.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderOzav).Return(
		&models.CustomerProviderIntegration{
			CustomerID: 7, Provider: models.ProviderOzav,
			Status: models.IntegrationStatusActive, ExternalCustomerID: support.Ptr("ozav_cus_1"),
			Metadata: "{}",
		}, nil).Once()

	profiles := mocks.NewMockCustomerIndividualProfileRepository(t)

	router := ramp.NewRouter(ramp.RouterDeps{Factory: ramp.NewFactory(adapter), Config: ramp.NewConfig(settings),
		Integrations: integ, Kyc: kycs, Persons: ramp.Persons{Customers: support.IndividualCustomers(t), Profiles: profiles}, AppEnv: "local"})

	resolved, ref, err := router.ResolveForSettlement(context.Background(), 7, models.ProviderOzav)
	require.NoError(t, err)
	require.Equal(t, models.ProviderOzav, resolved.Code())
	require.Equal(t, "ozav_cus_1", ref.ExternalID)

	// The proof is in what was never consulted: no provider setting, no KYC
	// row, no retained identity. Any one would be an eligibility answer
	// standing between the user and money they already paid.
	settings.AssertNotCalled(t, "Bool", testifymock.Anything, testifymock.Anything)
	kycs.AssertNotCalled(t, "FindByCustomerAndProvider", testifymock.Anything, testifymock.Anything)
	profiles.AssertNotCalled(t, "Find", testifymock.Anything)
}

// Without a customer there is nothing to call the provider with, and that is a
// real failure rather than a skip.
func TestResolveForSettlementRefusesWithoutACustomer(t *testing.T) {
	adapter := mocks.NewMockProviderAdapter(t)
	adapter.EXPECT().Code().Return(models.ProviderOzav).Maybe()
	integ := mocks.NewMockCustomerProviderIntegrationRepository(t)
	integ.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderOzav).Return(nil, nil).Once()

	router := ramp.NewRouter(ramp.RouterDeps{Factory: ramp.NewFactory(adapter),
		Config: ramp.NewConfig(mocks.NewMockSettingsSource(t)), Integrations: integ,
		Kyc:     mocks.NewMockCustomerProviderKycRepository(t),
		Persons: ramp.Persons{Customers: support.IndividualCustomers(t), Profiles: mocks.NewMockCustomerIndividualProfileRepository(t)}, AppEnv: "local"})

	_, _, err := router.ResolveForSettlement(context.Background(), 7, models.ProviderOzav)
	require.ErrorIs(t, err, ramp.ErrIntegrationRequired)
}

// --- capability-scoped eligibility ------------------------------------------

// A user asking for a token only one provider serves is named that provider,
// rather than told that nobody serves the token at all — §1.2 of the design.
//
// This pins the naming, not the guard ordering: Bridge is approved here, so its
// KYC branch produces nothing nameable wherever Supports sits.
// TestResolveDoesNotBlameTheKycOfAProviderThatWouldNotServeTheToken is what
// pins the ordering.
func TestResolveNamesTheProviderThatWouldServeTheToken(t *testing.T) {
	h := newHarness(t)
	const userID = uint(1)
	// Approved everywhere, but it cannot price USDT.
	h.eligibleServing(t, userID, models.ProviderBridge, servesToken("USDC"))
	// Serves USDT, and this user has not finished its KYC.
	h.blockedByKyc(t, userID, models.ProviderOzav, models.KycStatusIncomplete, servesEverything)
	h.order(models.RailPixPayout, models.ProviderBridge, models.ProviderOzav)

	_, _, err := h.router().Resolve(context.Background(), userID, ramp.Capability{
		Rail: ramp.RailPixPayout, Token: "USDT", Chain: "solana"})

	var blocked *ramp.KycBlocked
	require.ErrorAs(t, err, &blocked)
	require.ErrorIs(t, err, ramp.ErrKycPending)
	require.Equal(t, models.ProviderOzav, blocked.Provider)
	require.Equal(t, models.KycStatusIncomplete, blocked.Status)
}

// The guard ordering itself, and the only wiring that can pin it: a single
// provider, blocked on KYC, that does not serve the token asked for.
//
// With Supports before the integration and KYC guards — the order this delivery
// introduces — Bridge leaves the chain as `unavailable`, nothing is nameable,
// and the answer is no_provider_available with no wizard. Move Supports below
// the KYC guard and the same wiring yields KycBlocked{bridge, pending}: a user
// sent to finish the KYC of a provider that could never have priced USDT, which
// is §1.2 of the design restated as a bug.
func TestResolveDoesNotBlameTheKycOfAProviderThatWouldNotServeTheToken(t *testing.T) {
	h := newHarness(t)
	const userID = uint(1)
	h.blockedByKyc(t, userID, models.ProviderBridge, models.KycStatusIncomplete, servesToken("USDC"))
	h.order(models.RailPixPayout, models.ProviderBridge)

	_, _, err := h.router().Resolve(context.Background(), userID, ramp.Capability{
		Rail: ramp.RailPixPayout, Token: "USDT", Chain: "solana"})

	require.ErrorIs(t, err, ramp.ErrNoProviderAvailable)
	var blocked *ramp.KycBlocked
	require.False(t, errors.As(err, &blocked),
		"the capability filter must run before the KYC guard, or a refusal names a provider that would never have served the token")
}

// The most ACTIONABLE refusal wins, not the furthest one. A dead end must never
// beat a path the user can walk.
func TestResolvePrefersAPendingProviderOverARejectedOne(t *testing.T) {
	h := newHarness(t)
	const userID = uint(1)
	h.blockedByKyc(t, userID, models.ProviderBridge, models.KycStatusRejected, servesEverything)
	h.blockedByKyc(t, userID, models.ProviderOzav, models.KycStatusIncomplete, servesEverything)
	h.order(models.RailPixPayout, models.ProviderBridge, models.ProviderOzav)

	_, _, err := h.router().Resolve(context.Background(), userID, ramp.Capability{
		Rail: ramp.RailPixPayout, Token: "USDC", Chain: "solana"})

	var blocked *ramp.KycBlocked
	require.ErrorAs(t, err, &blocked)
	require.ErrorIs(t, err, ramp.ErrKycPending)
	require.Equal(t, models.ProviderOzav, blocked.Provider,
		"a dead end must not outrank a path the user can walk")
}

// Only rejections left: then it is the dead end, and it says so rather than
// opening a wizard that resolves nothing.
func TestResolveReportsKycRejectedWhenNobodyPendingCanServe(t *testing.T) {
	h := newHarness(t)
	const userID = uint(1)
	h.blockedByKyc(t, userID, models.ProviderBridge, models.KycStatusRejected, servesEverything)
	h.order(models.RailPixPayout, models.ProviderBridge)

	_, _, err := h.router().Resolve(context.Background(), userID, ramp.Capability{
		Rail: ramp.RailPixPayout, Token: "USDC", Chain: "solana"})

	var blocked *ramp.KycBlocked
	require.ErrorAs(t, err, &blocked)
	require.ErrorIs(t, err, ramp.ErrKycRejected)
	require.NotErrorIs(t, err, ramp.ErrKycPending)
	require.Equal(t, models.ProviderBridge, blocked.Provider)
	require.Equal(t, models.KycStatusRejected, blocked.Status)
}

// The endorsement check is still the gate and the status is still only how the
// refusal is worded. CustomerProviderKyc.SupportsRail refuses anything that is not
// `approved`, so a rejected row never routes however its endorsements read —
// which is what makes reading KycStatus inside that branch safe rather than a
// second, competing gate.
func TestResolveReportsARejectedRowAsRejectedEvenWithApprovedEndorsements(t *testing.T) {
	h := newHarness(t)
	const userID = uint(1)
	h.realAdapter(t, models.ProviderBridge)
	h.config(models.ProviderBridge, true, models.ProviderModeSandbox)
	h.integration(userID, models.ProviderBridge, ptr("cus_bridge"), models.IntegrationStatusActive)
	h.kycWithStatus(userID, models.ProviderBridge, models.KycStatusRejected, approvedPix)
	h.order(models.RailPixPayout, models.ProviderBridge)

	_, _, err := h.router().Resolve(context.Background(), userID, ramp.Capability{
		Rail: ramp.RailPixPayout, Token: "USDC"})
	require.ErrorIs(t, err, ramp.ErrKycRejected)
	require.NotErrorIs(t, err, ramp.ErrKycPending)
}

// The admin's rail order stays sovereign. A provider it excludes is not a
// provider the user can unblock, so no wizard is opened for it.
func TestResolveDoesNotReachAProviderTheRailOrderExcludes(t *testing.T) {
	h := newHarness(t)
	const userID = uint(1)
	h.eligibleServing(t, userID, models.ProviderBridge, servesToken("USDC"))
	// Approved and the only one serving USDT — and left out of the rail order.
	h.eligible(t, userID, models.ProviderOzav)
	h.order(models.RailPixPayout, models.ProviderBridge)

	_, _, err := h.router().Resolve(context.Background(), userID, ramp.Capability{
		Rail: ramp.RailPixPayout, Token: "USDT", Chain: "solana"})

	require.ErrorIs(t, err, ramp.ErrNoProviderAvailable)
	var blocked *ramp.KycBlocked
	require.False(t, errors.As(err, &blocked), "no wizard is opened for an admin's decision")
}

// The sticky path resolves on the rail alone: between an estimate and the
// create that redeems it, there is no token left to re-filter on.
func TestResolveProviderAsksTheAdapterAboutTheRailAlone(t *testing.T) {
	h := newHarness(t)
	const userID = uint(1)
	h.eligibleServing(t, userID, models.ProviderBridge, func(cap ramp.Capability) bool {
		require.Empty(t, cap.Token, "the sticky path must not re-decide the token")
		require.Empty(t, cap.Chain)
		return cap.Rail == ramp.RailPixPayout
	})

	adapter, _, err := h.router().ResolveProvider(context.Background(), userID,
		ramp.RailPixPayout, models.ProviderBridge)
	require.NoError(t, err)
	require.Equal(t, models.ProviderBridge, adapter.Code())
}

// Approved anywhere is approved: a provider ahead in the rail order that
// rejected the user — for a reason they can fix or not — does not keep the
// one that approved them from serving, on either rail. kyc_rejected is only
// for a rail nobody can serve.
func TestResolveServesThroughTheApprovedProviderPastARejectedOne(t *testing.T) {
	for _, rail := range []string{models.RailPixPayout, models.RailPixPayin} {
		t.Run(rail, func(t *testing.T) {
			h := newHarness(t)
			const userID = uint(1)
			h.blockedByKyc(t, userID, models.ProviderBridge, models.KycStatusRejected, servesEverything)
			h.eligible(t, userID, models.ProviderOzav)
			h.order(rail, models.ProviderBridge, models.ProviderOzav)

			adapter, ref, err := h.router().Resolve(context.Background(), userID, ramp.Capability{
				Rail: ramp.Rail(rail), Token: "USDC", Chain: "solana"})
			require.NoError(t, err)
			require.Equal(t, models.ProviderOzav, adapter.Code())
			require.Equal(t, "cus_"+models.ProviderOzav, ref.ExternalID)
		})
	}
}

// --- eligibility per person ---------------------------------------------------

// policed is a mock adapter under a jurisdiction policy, the way the real
// Bridge and Ozav adapters declare theirs (ramp.JurisdictionPolicy).
type policed struct {
	*mocks.MockProviderAdapter
	rails func(ramp.TaxIdentity, string) []ramp.Rail
}

func (p policed) RailsFor(identity ramp.TaxIdentity, residence string) []ramp.Rail {
	return p.rails(identity, residence)
}

// bridgePolicy is Bridge's in miniature: both rails for a CPF, the payout
// alone for a passport.
func bridgePolicy(identity ramp.TaxIdentity, _ string) []ramp.Rail {
	switch identity.Type {
	case models.TaxIDTypeCPF:
		return []ramp.Rail{ramp.RailPixPayin, ramp.RailPixPayout}
	case models.TaxIDTypePassport:
		return []ramp.Rail{ramp.RailPixPayout}
	}
	return nil
}

// ozavPolicy is Ozav's: both rails for a CPF holder living in Brazil, nothing
// for anyone else.
func ozavPolicy(identity ramp.TaxIdentity, residence string) []ramp.Rail {
	if identity.Type == models.TaxIDTypeCPF && residence == "BRA" {
		return []ramp.Rail{ramp.RailPixPayin, ramp.RailPixPayout}
	}
	return nil
}

// police puts the adapter registered last under a policy.
func (h *harness) police(rails func(ramp.TaxIdentity, string) []ramp.Rail) {
	last := len(h.adapters) - 1
	h.adapters[last] = policed{h.adapters[last].(*mocks.MockProviderAdapter), rails}
}

var (
	// An Argentine passport held in Argentina.
	passportHolder = &ramp.Profile{TaxID: "AB123456", TaxIDType: models.TaxIDTypePassport, TaxIDCountry: "ARG",
		Address: ramp.Address{Country: "ARG"}}
	// An installed build's profile: no type, no issuing country, BRA.
	cpfInBrazil = &ramp.Profile{TaxID: "39053344705", Address: ramp.Address{Country: "BRA"}}
	// A CPF holder who lives in the United Kingdom.
	cpfInTheUK = &ramp.Profile{TaxID: "39053344705", TaxIDType: models.TaxIDTypeCPF, TaxIDCountry: "BRA",
		Address: ramp.Address{Country: "GBR"}}
)

// A passport holder Bridge approved — umbrella pix and all — is refused the
// deposit by who they are, before the row is read: the endorsements would
// have cleared it, and Bridge's sandbox does open a BRL account for a
// passport. The payout, which the passport does open, still resolves.
func TestResolveRefusesARailTheRetainedIdentityCannotOpen(t *testing.T) {
	h := newHarness(t)
	const userID = uint(1)
	h.eligible(t, userID, models.ProviderBridge)
	h.police(bridgePolicy)
	h.retained = passportHolder
	h.order(models.RailPixPayin, models.ProviderBridge)
	h.order(models.RailPixPayout, models.ProviderBridge)
	router := h.router()

	_, _, err := router.Resolve(context.Background(), userID, ramp.Capability{Rail: ramp.RailPixPayin, Token: "USDC"})
	require.ErrorIs(t, err, ramp.ErrRailNotEligible)
	require.Equal(t, "rail_not_eligible", ramp.ErrorCode(err))
	var blocked *ramp.KycBlocked
	require.False(t, errors.As(err, &blocked), "no provider to send the user to: no wizard step opens the rail")

	adapter, ref, err := router.Resolve(context.Background(), userID, ramp.Capability{Rail: ramp.RailPixPayout, Token: "USDC"})
	require.NoError(t, err)
	require.Equal(t, models.ProviderBridge, adapter.Code())
	require.Equal(t, "cus_"+models.ProviderBridge, ref.ExternalID)
}

// The same wiring for a CPF holder in Brazil, and for a user who retained no
// profile (the policy below refuses everyone, and is not asked): nothing
// changes.
func TestResolveIsUnchangedForACpfInBrazilAndWithoutARetainedProfile(t *testing.T) {
	for name, tc := range map[string]struct {
		retained *ramp.Profile
		policy   func(ramp.TaxIdentity, string) []ramp.Rail
	}{
		"a cpf in brazil": {cpfInBrazil, bridgePolicy},
		"no retained profile": {nil, func(ramp.TaxIdentity, string) []ramp.Rail {
			t.Fatal("without a retained profile there is nobody to ask the policy about")
			return nil
		}},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			const userID = uint(1)
			h.eligible(t, userID, models.ProviderBridge)
			h.police(tc.policy)
			h.retained = tc.retained
			h.order(models.RailPixPayin, models.ProviderBridge)

			adapter, _, err := h.router().Resolve(context.Background(), userID,
				ramp.Capability{Rail: ramp.RailPixPayin, Token: "USDC"})
			require.NoError(t, err)
			require.Equal(t, models.ProviderBridge, adapter.Code())
		})
	}
}

// Eligibility sits right after Supports, before the integration: a passport
// holder who never linked Bridge is asked to link it for the payout, and is
// told the deposit is not theirs to have — not sent to link a provider for a
// rail it would refuse them anyway.
func TestResolveRefusesTheRailBeforeAskingForAnIntegration(t *testing.T) {
	h := newHarness(t)
	const userID = uint(1)
	h.realAdapter(t, models.ProviderBridge)
	h.police(bridgePolicy)
	h.config(models.ProviderBridge, true, models.ProviderModeSandbox)
	h.noIntegration(userID, models.ProviderBridge)
	h.retained = passportHolder
	h.order(models.RailPixPayin, models.ProviderBridge)
	h.order(models.RailPixPayout, models.ProviderBridge)
	router := h.router()

	_, _, err := router.Resolve(context.Background(), userID, ramp.Capability{Rail: ramp.RailPixPayout, Token: "USDC"})
	require.ErrorIs(t, err, ramp.ErrIntegrationRequired)

	_, _, err = router.Resolve(context.Background(), userID, ramp.Capability{Rail: ramp.RailPixPayin, Token: "USDC"})
	require.ErrorIs(t, err, ramp.ErrRailNotEligible)
	require.NotErrorIs(t, err, ramp.ErrIntegrationRequired)
}

// The identity and residence are read once per resolution, never once per
// provider in the rail order — on both walks of it.
func TestResolveReadsTheRetainedProfileOncePerResolution(t *testing.T) {
	for name, resolve := range map[string]func(*ramp.Router) error{
		"Resolve": func(r *ramp.Router) error {
			_, _, err := r.Resolve(context.Background(), 1, ramp.Capability{Rail: ramp.RailPixPayout, Token: "USDC"})
			return err
		},
		"ResolveWithCall": func(r *ramp.Router) error {
			_, err := r.ResolveWithCall(context.Background(), 1, ramp.Capability{Rail: ramp.RailPixPayout, Token: "USDC"},
				ramp.FailoverOnAnyError, func(context.Context, ramp.ProviderAdapter, ramp.CustomerRef) error { return nil })
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			const userID = uint(1)
			h.blockedByKyc(t, userID, models.ProviderOzav, models.KycStatusIncomplete, servesEverything)
			h.police(ozavPolicy)
			h.eligible(t, userID, models.ProviderBridge)
			h.police(bridgePolicy)
			h.order(models.RailPixPayout, models.ProviderOzav, models.ProviderBridge)
			h.profiles.EXPECT().Find(userID).Return(cpfInBrazil, nil).Once()

			require.NoError(t, resolve(h.router()))
		})
	}
}

// A profile that cannot be read is a failure to decide, never "nobody in
// particular": that would route a passport to a rail it does not open.
func TestResolveFailsWhenTheRetainedProfileCannotBeRead(t *testing.T) {
	h := newHarness(t)
	const userID = uint(1)
	h.eligible(t, userID, models.ProviderBridge)
	h.police(bridgePolicy)
	h.order(models.RailPixPayin, models.ProviderBridge)
	broken := errors.New("user kyc profile repo: connection reset")
	h.profiles.EXPECT().Find(userID).Return(nil, broken).Once()

	_, _, err := h.router().Resolve(context.Background(), userID, ramp.Capability{Rail: ramp.RailPixPayin, Token: "USDC"})
	require.ErrorIs(t, err, broken)
	require.NotErrorIs(t, err, ramp.ErrRailNotEligible)
}

// The provider's own word counts the same: an approved customer whose
// directional deposit endorsement Bridge marked not available in their
// region (a CPF living abroad) is not eligible for it — not pending,
// which would open a wizard nothing typed in it can satisfy.
func TestResolveReadsADirectionalEndorsementNotAvailableAsNotEligible(t *testing.T) {
	for name, retained := range map[string]*ramp.Profile{
		"a cpf in the uk":     cpfInTheUK,
		"no retained profile": nil,
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			const userID = uint(1)
			h.realAdapter(t, models.ProviderBridge)
			h.police(bridgePolicy)
			h.config(models.ProviderBridge, true, models.ProviderModeSandbox)
			h.integration(userID, models.ProviderBridge, ptr("cus_bridge"), models.IntegrationStatusActive)
			h.kyc(userID, models.ProviderBridge, `[{"name":"pix","status":"approved"},`+
				`{"name":"pix_offramp","status":"approved"},{"name":"pix_onramp","status":"not_available"}]`)
			h.retained = retained
			h.order(models.RailPixPayin, models.ProviderBridge)

			_, _, err := h.router().Resolve(context.Background(), userID, ramp.Capability{Rail: ramp.RailPixPayin, Token: "USDC"})
			require.ErrorIs(t, err, ramp.ErrRailNotEligible)
			require.NotErrorIs(t, err, ramp.ErrKycPending)
		})
	}
}

// Not available is the provider's final word only on an approved row. A row
// still owing something is pending: finishing it is a step the user can
// take, and it says nothing yet about the rail.
func TestResolveKeepsAnUnfinishedRowPendingWhateverItsDirectionalSays(t *testing.T) {
	h := newHarness(t)
	const userID = uint(1)
	h.realAdapter(t, models.ProviderBridge)
	h.config(models.ProviderBridge, true, models.ProviderModeSandbox)
	h.integration(userID, models.ProviderBridge, ptr("cus_bridge"), models.IntegrationStatusActive)
	h.kycWithStatus(userID, models.ProviderBridge, models.KycStatusIncomplete,
		`[{"name":"pix_onramp","status":"not_available"}]`)
	h.order(models.RailPixPayin, models.ProviderBridge)

	_, _, err := h.router().Resolve(context.Background(), userID, ramp.Capability{Rail: ramp.RailPixPayin, Token: "USDC"})
	require.ErrorIs(t, err, ramp.ErrKycPending)
}

// rail_not_eligible ranks above nothing but unavailable: it is only the
// answer when nothing the user can act on — nor a provider's refusal of the
// customer — is left in the rail order.
func TestResolveRanksNotEligibleBetweenUnavailableAndRejected(t *testing.T) {
	const userID = uint(1)
	payin := ramp.Capability{Rail: ramp.RailPixPayin, Token: "USDC"}

	t.Run("not eligible outranks an unconfigured provider", func(t *testing.T) {
		h := newHarness(t)
		h.realAdapter(t, models.ProviderOzav)
		h.config(models.ProviderOzav, false, models.ProviderModeSandbox)
		h.eligible(t, userID, models.ProviderBridge)
		h.police(bridgePolicy)
		h.retained = passportHolder
		h.order(models.RailPixPayin, models.ProviderOzav, models.ProviderBridge)

		_, _, err := h.router().Resolve(context.Background(), userID, payin)
		require.ErrorIs(t, err, ramp.ErrRailNotEligible)
	})

	t.Run("a rejection outranks not eligible", func(t *testing.T) {
		h := newHarness(t)
		h.blockedByKyc(t, userID, models.ProviderBridge, models.KycStatusRejected, servesEverything)
		h.police(bridgePolicy)
		h.eligible(t, userID, models.ProviderOzav)
		h.police(ozavPolicy)
		h.retained = cpfInTheUK
		h.order(models.RailPixPayin, models.ProviderOzav, models.ProviderBridge)

		_, _, err := h.router().Resolve(context.Background(), userID, payin)
		require.ErrorIs(t, err, ramp.ErrKycRejected)
	})

	t.Run("a pending provider outranks not eligible", func(t *testing.T) {
		h := newHarness(t)
		h.eligible(t, userID, models.ProviderOzav)
		h.police(ozavPolicy)
		h.blockedByKyc(t, userID, models.ProviderBridge, models.KycStatusIncomplete, servesEverything)
		h.police(bridgePolicy)
		h.retained = cpfInTheUK
		h.order(models.RailPixPayin, models.ProviderOzav, models.ProviderBridge)

		_, _, err := h.router().Resolve(context.Background(), userID, payin)
		var blocked *ramp.KycBlocked
		require.ErrorAs(t, err, &blocked)
		require.ErrorIs(t, err, ramp.ErrKycPending)
		require.Equal(t, models.ProviderBridge, blocked.Provider)
	})
}

// The read-back path reads no identity: an order's status, the reconciler,
// a standing account's destination must finish what exists (CLAUDE.md:
// reading back does not go through the router's eligibility), and an
// identity switched since must not strand them.
func TestResolveProviderDoesNotReadTheRetainedIdentity(t *testing.T) {
	h := newHarness(t)
	const userID = uint(1)
	h.eligible(t, userID, models.ProviderBridge)
	h.police(bridgePolicy)
	h.profiles.EXPECT().Find(testifymock.Anything).Return(passportHolder, nil).Maybe()

	adapter, _, err := h.router().ResolveProvider(context.Background(), userID, ramp.RailPixPayin, models.ProviderBridge)
	require.NoError(t, err)
	require.Equal(t, models.ProviderBridge, adapter.Code())
	h.profiles.AssertNotCalled(t, "Find", testifymock.Anything)
}

// Redeeming an estimate asks who the person is now, once. A Bridge estimate
// carries no expiry, so one priced while the user declared a CPF is still
// redeemable after they switch to a passport — and the umbrella pix the row
// carries would clear the deposit. The payout the passport opens still
// redeems.
func TestResolveRedemptionRefusesARailTheRetainedIdentityCannotOpen(t *testing.T) {
	h := newHarness(t)
	const userID = uint(1)
	h.eligible(t, userID, models.ProviderBridge)
	h.police(bridgePolicy)
	h.profiles.EXPECT().Find(userID).Return(passportHolder, nil).Twice()
	router := h.router()

	_, _, err := router.ResolveRedemption(context.Background(), userID, ramp.RailPixPayin, models.ProviderBridge)
	require.ErrorIs(t, err, ramp.ErrRailNotEligible)
	var blocked *ramp.KycBlocked
	require.False(t, errors.As(err, &blocked), "no provider to send the user to")

	adapter, ref, err := router.ResolveRedemption(context.Background(), userID, ramp.RailPixPayout, models.ProviderBridge)
	require.NoError(t, err)
	require.Equal(t, models.ProviderBridge, adapter.Code())
	require.Equal(t, "cus_"+models.ProviderBridge, ref.ExternalID)
}

// Without a retained profile, and for a CPF in Brazil, redemption is the
// sticky path it always was; a profile that cannot be read fails it.
func TestResolveRedemptionIsUnchangedWithoutAnIdentityThatRefuses(t *testing.T) {
	for name, retained := range map[string]*ramp.Profile{
		"no retained profile": nil,
		"a cpf in brazil":     cpfInBrazil,
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			const userID = uint(1)
			h.eligible(t, userID, models.ProviderBridge)
			h.police(bridgePolicy)
			h.retained = retained

			adapter, _, err := h.router().ResolveRedemption(context.Background(), userID,
				ramp.RailPixPayin, models.ProviderBridge)
			require.NoError(t, err)
			require.Equal(t, models.ProviderBridge, adapter.Code())
		})
	}

	t.Run("an unreadable profile", func(t *testing.T) {
		h := newHarness(t)
		const userID = uint(1)
		h.eligible(t, userID, models.ProviderBridge)
		broken := errors.New("user kyc profile repo: connection reset")
		h.profiles.EXPECT().Find(userID).Return(nil, broken).Once()

		_, _, err := h.router().ResolveRedemption(context.Background(), userID, ramp.RailPixPayin, models.ProviderBridge)
		require.ErrorIs(t, err, broken)
	})
}

// A company is resolved as who it declared: until the business tables say
// who that is, the router refuses it instead of reading it as nobody in
// particular — which every provider opens every rail to, Ozav included.
func TestResolveRefusesABusinessNothingSaysWhoItIs(t *testing.T) {
	h := newHarness(t)
	const company = uint(1_000_000_000_000)
	h.customers = mocks.NewMockCustomerRepository(t)
	h.customers.EXPECT().FindByID(company).Return(&models.Customer{Type: models.CustomerTypeBusiness}, nil).Once()
	h.eligible(t, company, models.ProviderOzav)
	h.order(models.RailPixPayin, models.ProviderOzav)

	_, _, err := h.router().Resolve(context.Background(), company, ramp.Capability{Rail: ramp.RailPixPayin})

	require.ErrorIs(t, err, ramp.ErrBusinessPersonUnknown)
}

// preparingProvider is a provider that prepares the funding of a payout.
type preparingProvider struct {
	*mocks.MockProviderAdapter
	*mocks.MockOfframpPrepareAdapter
}

// eligiblePreparing is eligible for a provider that also prepares funding.
func (h *harness) eligiblePreparing(t *testing.T, userID uint, code string) {
	t.Helper()
	inner := mocks.NewMockProviderAdapter(t)
	inner.EXPECT().Code().Return(code).Maybe()
	inner.EXPECT().Supports(testifymock.Anything).Return(true).Maybe()
	h.adapters = append(h.adapters, &preparingProvider{MockProviderAdapter: inner, MockOfframpPrepareAdapter: mocks.NewMockOfframpPrepareAdapter(t)})
	h.config(code, true, models.ProviderModeSandbox)
	h.integration(userID, code, ptr("cus_"+code), models.IntegrationStatusActive)
	h.kyc(userID, code, `[{"name":"pix","status":"approved"},{"name":"bank_payout","status":"approved"}]`)
}

func TestEvaluateSkipsAPreparingProviderOnPixPayoutUnlessTheCallerAcceptsPrepared(t *testing.T) {
	const userID = uint(1)
	for _, tc := range []struct {
		name     string
		rail     ramp.Rail
		prepared bool
		serves   bool
	}{
		{"pix payout, caller cannot sign", ramp.RailPixPayout, false, false},
		{"pix payout, caller declared prepared", ramp.RailPixPayout, true, true},
		{"pix payin is never affected", ramp.RailPixPayin, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.eligiblePreparing(t, userID, models.ProviderBlindPay)
			h.order(tc.rail.String(), models.ProviderBlindPay)

			adapter, _, err := h.router().Resolve(context.Background(), userID,
				ramp.Capability{Rail: tc.rail, PreparedFunding: tc.prepared})

			if tc.serves {
				require.NoError(t, err)
				require.Equal(t, models.ProviderBlindPay, adapter.Code())
				return
			}
			require.ErrorIs(t, err, ramp.ErrNoProviderAvailable)
		})
	}
}
