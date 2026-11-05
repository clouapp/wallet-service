package kyc_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/goravel/framework/testing/mock"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/featureflags/catalog"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/kyc"
	"github.com/macrowallets/waas/packages/rampengine/packages/featureflag"
	"github.com/macrowallets/waas/packages/rampengine/packages/featureflag/contracts"
	"github.com/macrowallets/waas/packages/rampengine/packages/featureflag/drivers"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
	"github.com/macrowallets/waas/packages/rampengine/tests/support"
)

// openFeatures is the flag catalogue on an in-memory store. Every rail reads
// open unless a test closes it — the money path's own default, since an
// operator has to switch a rail off on purpose.
func openFeatures() contracts.Feature {
	m := featureflag.NewManager(drivers.NewMemory())
	catalog.Register(m)
	return m
}

// capHarness bundles the collaborators CapabilitiesService needs. Config and
// Factory are concrete types (not interfaces), so they are built for real,
// the same way router_test.go does it, over a mocked SettingsSource and
// MockProviderAdapter stand-ins; Resolve itself is a mock, since that is the
// one boundary this service is unit-tested against.
type capHarness struct {
	t         *testing.T
	resolver  *mocks.MockCapabilityResolver
	features  contracts.Feature
	kycRepo   *mocks.MockCustomerProviderKycRepository
	integRepo *mocks.MockCustomerProviderIntegrationRepository
	profiles  *mocks.MockCustomerIndividualProfileRepository
	steps     *mocks.MockStepsSource
	settings  *mocks.MockSettingsSource
	adapters  []ramp.ProviderAdapter
	// retained is the profile the user retained; nil, none — the banner lists
	// every eligible provider then, as it did before eligibility was per
	// person.
	retained *ramp.Profile
}

func newCapHarness(t *testing.T) *capHarness {
	t.Helper()
	// An unreadable rail switch is logged before it reads closed.
	mock.Factory().Log()
	return &capHarness{
		t:         t,
		resolver:  mocks.NewMockCapabilityResolver(t),
		features:  openFeatures(),
		kycRepo:   mocks.NewMockCustomerProviderKycRepository(t),
		integRepo: mocks.NewMockCustomerProviderIntegrationRepository(t),
		profiles:  mocks.NewMockCustomerIndividualProfileRepository(t),
		steps:     mocks.NewMockStepsSource(t),
		settings:  mocks.NewMockSettingsSource(t),
	}
}

// enable registers a provider as enabled+sandbox and gives it a real (not
// stub) adapter in the factory, so it survives both EligibleProviders() and
// IsImplemented().
func (h *capHarness) enable(code string) {
	h.t.Helper()
	group := ramp.ProviderGroup(code)
	h.settings.EXPECT().Bool(group, ramp.SettingEnabled).Return(true, nil).Maybe()
	h.settings.EXPECT().String(group, ramp.SettingMode).Return("sandbox", nil).Maybe()
	adapter := mocks.NewMockProviderAdapter(h.t)
	adapter.EXPECT().Code().Return(code).Maybe()
	h.adapters = append(h.adapters, adapter)
}

// disable registers a provider as switched off — the settings answer
// EligibleProviders() needs for every code in ramp.AllProviderCodes(), even
// one this test does not care about.
func (h *capHarness) disable(code string) {
	h.t.Helper()
	group := ramp.ProviderGroup(code)
	h.settings.EXPECT().Bool(group, ramp.SettingEnabled).Return(false, nil).Maybe()
	h.settings.EXPECT().String(group, ramp.SettingMode).Return("sandbox", nil).Maybe()
}

// disableEveryUndeclaredProvider fills in AllProviderCodes() with "disabled"
// for whichever ones the test has not already called enable() on, so
// EligibleProviders()'s sweep over the full vocabulary never hits an
// unexpected-call panic.
func (h *capHarness) disableEveryUndeclaredProvider(declared ...string) {
	seen := map[string]bool{}
	for _, code := range declared {
		seen[code] = true
	}
	for _, code := range ramp.AllProviderCodes() {
		if !seen[code] {
			h.disable(code)
		}
	}
}

// openBank opens the bank payout switch, which the catalogue ships closed. The
// tests that stand for "every rail resolves the same way" open it so the bank
// rows join the product; the others leave it closed and see them as disabled.
func (h *capHarness) openBank() {
	h.t.Helper()
	// The global is a veto and the user's own override the grant: both open.
	require.NoError(h.t, h.features.Activate(context.Background(), ramp.RailACHPayout.Flag(), nil))
	require.NoError(h.t, h.features.Activate(context.Background(), ramp.RailACHPayout.Flag(), models.UserFlagScope(1)))
}

func (h *capHarness) service() *kyc.CapabilitiesService {
	h.profiles.EXPECT().Find(uint(1)).
		RunAndReturn(func(uint) (*ramp.Profile, error) { return h.retained, nil }).Maybe()
	return kyc.NewCapabilitiesService(kyc.CapabilitiesDeps{
		Resolver:     h.resolver,
		Features:     h.features,
		Config:       ramp.NewConfig(h.settings),
		Factory:      ramp.NewFactory(h.adapters...),
		Kyc:          h.kycRepo,
		Integrations: h.integRepo,
		Persons:      ramp.Persons{Customers: support.IndividualCustomers(h.t), Profiles: h.profiles},
		Steps:        h.steps,
		// "local": every enable()/disable() in this harness writes "sandbox",
		// and "local" never disagrees with sandbox, so EligibleProviders
		// narrows on enabled alone for every test in this file that does not
		// itself force a mismatch.
		AppEnv: "local",
	})
}

// seedRows is the user's whole KYC state, as the one query pendingProviders
// makes returns it. One expectation for every provider, not one each: asking
// per provider was the N+1 this listing used to pay on every page load.
func (h *capHarness) seedRows(rows ...models.CustomerProviderKyc) {
	h.t.Helper()
	h.kycRepo.EXPECT().ListByCustomer(uint(1)).Return(rows, nil).Once()
	// Nobody holds a customer unless a test says so (seedCustomers).
	h.integRepo.EXPECT().ListByCustomer(uint(1)).Return(nil, nil).Maybe()
}

// seedCustomers answers the integrations read with a customer at each of
// these providers. It has to run before seedRows, whose default answers
// "none".
func (h *capHarness) seedCustomers(providers ...string) {
	h.t.Helper()
	links := make([]models.CustomerProviderIntegration, 0, len(providers))
	for _, provider := range providers {
		id := "cus_" + provider
		links = append(links, models.CustomerProviderIntegration{CustomerID: 1, Provider: provider, ExternalCustomerID: &id})
	}
	h.integRepo.EXPECT().ListByCustomer(uint(1)).Return(links, nil).Maybe()
}

// row is one provider's stored compliance state, with the jsonb columns set
// to the empty documents the NOT NULL constraint requires — OutstandingFor
// decodes them, so a zero value here would not survive the real column.
func row(provider, status string, requirements ...string) models.CustomerProviderKyc {
	out := models.CustomerProviderKyc{
		CustomerID: 1, Provider: provider, KycStatus: status,
		Endorsements: "[]", Requirements: "[]", RejectionReasons: "[]", RawStatus: "{}",
	}
	if len(requirements) > 0 {
		out.SetRequirements(requirements)
	}
	return out
}

// alwaysResolves is the default: every capability in the 2-rail x 2-token
// product resolves through the given error (nil for available).
func (h *capHarness) alwaysResolves(err error) {
	h.resolver.EXPECT().
		Resolve(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(nil, ramp.CustomerRef{}, err).Maybe()
}

func TestUsableIsFalseWithZeroAvailableCapabilities(t *testing.T) {
	h := newCapHarness(t)
	h.enable(models.ProviderOzav)
	h.disableEveryUndeclaredProvider(models.ProviderOzav)
	h.alwaysResolves(&ramp.KycBlocked{Provider: models.ProviderOzav, Status: models.KycStatusIncomplete, Err: ramp.ErrKycPending})
	h.seedRows(row(models.ProviderOzav, models.KycStatusIncomplete))
	h.steps.EXPECT().PendingSteps(uint(1), models.ProviderOzav).Return([]string{"profile"})

	view, err := h.service().Compute(context.Background(), 1, 1)
	require.NoError(t, err)
	require.False(t, view.Usable)
	for _, cap := range view.Capabilities {
		require.False(t, cap.Available, "%+v", cap)
	}
}

func TestUsableIsTrueWithOneAvailableCapability(t *testing.T) {
	h := newCapHarness(t)
	h.enable(models.ProviderBridge)
	h.disableEveryUndeclaredProvider(models.ProviderBridge)
	h.seedRows(row(models.ProviderBridge, models.KycStatusApproved))

	served := ramp.Capability{Rail: ramp.RailPixPayin, Token: "USDC", Chain: "solana"}
	h.resolver.EXPECT().
		Resolve(testifymock.Anything, uint(1), testifymock.Anything).
		RunAndReturn(func(_ context.Context, _ uint, cap ramp.Capability) (ramp.ProviderAdapter, ramp.CustomerRef, error) {
			if cap == served {
				return nil, ramp.CustomerRef{}, nil
			}
			return nil, ramp.CustomerRef{}, ramp.ErrNoProviderAvailable
		})

	view, err := h.service().Compute(context.Background(), 1, 1)
	require.NoError(t, err)
	require.True(t, view.Usable)
}

// TestUsableIsFalseWhenAnApprovedProviderBlocksEveryCapability is the
// approved-with-zero-covering-endorsements edge the design calls out by
// name, and the reason Usable is derived from Capabilities rather than from
// "at least one provider approved" (see CapabilitiesView.Usable's own
// comment). A KYC row can be approved and still fail SupportsRail for every
// rail — no approved endorsement actually covers pix_payin or pix_payout —
// and the router reports that exactly like any other pending refusal:
// KycBlocked{Status: "approved", Err: ErrKycPending}. Without this test,
// nothing in the suite ever gives Resolve an approved-but-blocked row, so
// "usable derived from the list" and "usable derived from provider status"
// would both pass every other test here.
//
// The provider must also still surface in PendingProviders. Without
// blockedProviders widening that set (capabilities.go's own comment on the
// terminal && !blockedProviders[code] clause), an approved status reads as
// terminal and this provider would be silently dropped as "looks done" —
// exactly the row a support agent most needs to see, since nothing about its
// own status says it is stuck.
func TestUsableIsFalseWhenAnApprovedProviderBlocksEveryCapability(t *testing.T) {
	h := newCapHarness(t)
	h.enable(models.ProviderOzav)
	h.disableEveryUndeclaredProvider(models.ProviderOzav)
	h.alwaysResolves(&ramp.KycBlocked{
		Provider: models.ProviderOzav, Status: models.KycStatusApproved, Err: ramp.ErrKycPending,
	})
	h.seedRows(row(models.ProviderOzav, models.KycStatusApproved))
	h.steps.EXPECT().PendingSteps(uint(1), models.ProviderOzav).Return([]string{"endorsement"})

	view, err := h.service().Compute(context.Background(), 1, 1)
	require.NoError(t, err)
	require.False(t, view.Usable, "approved with no covering endorsement must not read as usable")
	for _, cap := range view.Capabilities {
		require.False(t, cap.Available, "%+v", cap)
	}

	require.Len(t, view.PendingProviders, 1,
		"an approved-but-blocked provider must still surface, not be dropped as terminal")
	require.Equal(t, models.ProviderOzav, view.PendingProviders[0].Provider)
	require.Equal(t, models.KycStatusApproved, view.PendingProviders[0].Status)
}

// TestRailDisabledCapabilitiesReadUnavailableWithoutNamingAProvider is §4.5's
// own requirement: every operation that actually creates something checks
// the rail's switch first (requireRail, ramps.go), and this listing
// must never promise a capability the very next call would refuse with 503
// rail_disabled. Bridge is approved and would otherwise cover every tuple in
// both rails; pix_payout is closed by flag regardless, so its two
// capabilities must read unavailable — with no provider named, since an
// operator's veto is not any one provider's KYC pendency — while pix_payin
// stays available and keeps the whole view usable.
func TestRailDisabledCapabilitiesReadUnavailableWithoutNamingAProvider(t *testing.T) {
	h := newCapHarness(t)
	h.enable(models.ProviderBridge)
	h.disableEveryUndeclaredProvider(models.ProviderBridge)
	require.NoError(t, h.features.Deactivate(context.Background(), ramp.RailPixPayout.Flag(), nil))
	h.seedRows(row(models.ProviderBridge, models.KycStatusApproved))

	// No pix_payout tuple may ever reach Resolve: a disabled rail is
	// short-circuited before the resolver is consulted at all, and this
	// expectation with no .Maybe()/wildcard rail would itself fail the test
	// if a pix_payout tuple ever reached it.
	h.resolver.EXPECT().
		Resolve(testifymock.Anything, uint(1), testifymock.MatchedBy(func(cap ramp.Capability) bool {
			return cap.Rail != ramp.RailPixPayout
		})).
		Return(nil, ramp.CustomerRef{}, nil)

	view, err := h.service().Compute(context.Background(), 1, 1)
	require.NoError(t, err)
	require.True(t, view.Usable, "pix_payin is still open and bridge covers it")

	var sawPayout int
	for _, cap := range view.Capabilities {
		if ramp.Rail(cap.Rail).IsBank() {
			// Closed by default: disabled, no provider, and no banner entry.
			require.False(t, cap.Available, "%+v", cap)
			require.Equal(t, &kyc.BlockedBy{Reason: "rail_disabled"}, cap.BlockedBy)
			continue
		}
		if cap.Rail != ramp.RailPixPayout.String() {
			require.True(t, cap.Available, "%+v", cap)
			continue
		}
		sawPayout++
		require.False(t, cap.Available, "%+v", cap)
		require.NotNil(t, cap.BlockedBy)
		require.Equal(t, "rail_disabled", cap.BlockedBy.Reason)
		require.Empty(t, cap.BlockedBy.Provider, "an operator veto names no provider")
		require.Empty(t, cap.BlockedBy.PendingSteps)
	}
	require.Equal(t, 3, sawPayout, "every pix_payout token must be covered")

	// Bridge is approved and never blocked by anything KYC-shaped here, so it
	// must not appear in the pending-providers banner either — a closed rail
	// is not a pendency of the provider's own state.
	require.Empty(t, view.PendingProviders)
}

func TestBlockedByCarriesProviderAndStepsForKycPending(t *testing.T) {
	h := newCapHarness(t)
	h.enable(models.ProviderOzav)
	h.disableEveryUndeclaredProvider(models.ProviderOzav)
	h.alwaysResolves(&ramp.KycBlocked{
		Provider: models.ProviderOzav, Status: models.KycStatusIncomplete,
		Err: ramp.ErrKycPending,
	})
	h.openBank()
	h.seedRows(row(models.ProviderOzav, models.KycStatusIncomplete))
	h.steps.EXPECT().PendingSteps(uint(1), models.ProviderOzav).
		Return([]string{"profile", "proof_of_address"}).Once()

	view, err := h.service().Compute(context.Background(), 1, 1)
	require.NoError(t, err)
	require.Len(t, view.Capabilities, 21, "7 rails x 3 tokens")
	for _, cap := range view.Capabilities {
		require.NotNil(t, cap.BlockedBy)
		require.Equal(t, "kyc_pending", cap.BlockedBy.Reason)
		require.Equal(t, models.ProviderOzav, cap.BlockedBy.Provider)
		require.Equal(t, []string{"profile", "proof_of_address"}, cap.BlockedBy.PendingSteps)
	}
}

// TestBlockedByCarriesNoPendingStepsForRejection mirrors ramps.go's
// enrichKycRefusal: a rejection is a dead end, so it names the provider but
// never asks PendingSteps for anything to fix.
func TestBlockedByCarriesNoPendingStepsForRejection(t *testing.T) {
	h := newCapHarness(t)
	h.enable(models.ProviderOzav)
	h.disableEveryUndeclaredProvider(models.ProviderOzav)
	h.alwaysResolves(&ramp.KycBlocked{
		Provider: models.ProviderOzav, Status: models.KycStatusRejected,
		Err: ramp.ErrKycRejected,
	})
	h.openBank()
	h.seedRows(row(models.ProviderOzav, models.KycStatusRejected))
	// No steps expectation at all: PendingSteps must never be called for a
	// rejection — an unexpected call on this mock fails the test.

	view, err := h.service().Compute(context.Background(), 1, 1)
	require.NoError(t, err)
	for _, cap := range view.Capabilities {
		require.NotNil(t, cap.BlockedBy)
		require.Equal(t, "kyc_rejected", cap.BlockedBy.Reason)
		require.Equal(t, models.ProviderOzav, cap.BlockedBy.Provider)
		require.Empty(t, cap.BlockedBy.PendingSteps)
	}
	// A rejected provider is a dead end, not a banner: never a wizard nobody
	// can move.
	require.Empty(t, view.PendingProviders)
}

// A rail the person's declaration does not open is a refusal the router
// decided, not a failure to decide: the listing shows it blocked, under its
// own code, naming no provider and no steps — there is no wizard that opens
// it — and the rails that are open stay available beside it.
func TestBlockedByNamesNoProviderForARailThePersonIsNotEligibleFor(t *testing.T) {
	h := newCapHarness(t)
	h.enable(models.ProviderBridge)
	h.disableEveryUndeclaredProvider(models.ProviderBridge)
	h.resolver.EXPECT().
		Resolve(testifymock.Anything, uint(1), testifymock.Anything).
		RunAndReturn(func(_ context.Context, _ uint, cap ramp.Capability) (ramp.ProviderAdapter, ramp.CustomerRef, error) {
			if cap.Rail == ramp.RailPixPayin {
				return nil, ramp.CustomerRef{}, fmt.Errorf("resolve: %w", ramp.ErrRailNotEligible)
			}
			return nil, ramp.CustomerRef{}, nil
		})
	h.seedRows(row(models.ProviderBridge, models.KycStatusApproved))
	// No steps expectation: nothing a wizard collects opens the rail, so
	// PendingSteps must never be asked.

	view, err := h.service().Compute(context.Background(), 1, 1)

	require.NoError(t, err)
	require.True(t, view.Usable)
	for _, cap := range view.Capabilities {
		if ramp.Rail(cap.Rail).IsBank() {
			// The bank switch is closed here, so the veto answers before the router.
			require.False(t, cap.Available, "%+v", cap)
			require.Equal(t, &kyc.BlockedBy{Reason: "rail_disabled"}, cap.BlockedBy)
			continue
		}
		if cap.Rail != ramp.RailPixPayin.String() {
			require.True(t, cap.Available, "%+v", cap)
			require.Nil(t, cap.BlockedBy)
			continue
		}
		require.False(t, cap.Available, "%+v", cap)
		if ramp.Rail(cap.Rail).IsBank() {
			// The bank switch is closed here, so the veto answers before the router.
			require.Equal(t, &kyc.BlockedBy{Reason: "rail_disabled"}, cap.BlockedBy)
			continue
		}
		require.Equal(t, &kyc.BlockedBy{Reason: "rail_not_eligible"}, cap.BlockedBy)
	}
	require.Empty(t, view.PendingProviders, "an approved provider owes nothing to finish")
}

// TestPendingProvidersDedupsAcrossCapabilities proves PendingSteps is not
// re-read once per capability: the mock's .Once() below would fail the test
// if Compute asked the same provider's steps twice.
func TestPendingProvidersDedupsAcrossCapabilities(t *testing.T) {
	h := newCapHarness(t)
	h.enable(models.ProviderOzav)
	h.disableEveryUndeclaredProvider(models.ProviderOzav)
	h.alwaysResolves(&ramp.KycBlocked{
		Provider: models.ProviderOzav, Status: models.KycStatusIncomplete,
		Err: ramp.ErrKycPending,
	})
	h.seedRows(row(models.ProviderOzav, models.KycStatusIncomplete))
	h.steps.EXPECT().PendingSteps(uint(1), models.ProviderOzav).Return([]string{"profile"}).Once()

	view, err := h.service().Compute(context.Background(), 1, 1)
	require.NoError(t, err)
	require.Len(t, view.PendingProviders, 1)
	require.Equal(t, models.ProviderOzav, view.PendingProviders[0].Provider)
	require.Equal(t, models.KycStatusIncomplete, view.PendingProviders[0].Status)
}

// TestPendingProvidersIncludesAShadowedNotStartedProvider is the case a
// blocked_by-only derivation misses entirely: Bridge is approved and covers
// every capability in the product, so Resolve never even reaches Ozav — the
// ordinary shape of "an admin just enabled a second provider" (design §4.7).
// Ozav must still reach the banner.
func TestPendingProvidersIncludesAShadowedNotStartedProvider(t *testing.T) {
	h := newCapHarness(t)
	h.enable(models.ProviderBridge)
	h.enable(models.ProviderOzav)
	h.disableEveryUndeclaredProvider(models.ProviderBridge, models.ProviderOzav)
	h.openBank()
	h.alwaysResolves(nil)                                            // bridge covers everything; nothing is ever blocked
	h.seedRows(row(models.ProviderBridge, models.KycStatusApproved)) // ozav: never submitted, so no row at all
	// No PendingSteps expectation: a provider with no row has no steps to
	// derive, and the listing no longer spends a lookup discovering that.

	view, err := h.service().Compute(context.Background(), 1, 1)
	require.NoError(t, err)
	require.True(t, view.Usable)
	for _, cap := range view.Capabilities {
		require.True(t, cap.Available, "bridge alone should cover every capability: %+v", cap)
	}
	require.Len(t, view.PendingProviders, 1)
	require.Equal(t, models.ProviderOzav, view.PendingProviders[0].Provider)
	require.Equal(t, models.KycStatusNotStarted, view.PendingProviders[0].Status)
	// The banner is the new provider's entrance, and what it opens is the
	// whole application: nothing was ever sent to it.
	require.Equal(t, []string{"personal", "profile", "address", "document", "selfie"},
		view.PendingProviders[0].PendingSteps)
}

func TestPendingProvidersExcludesApprovedAndRejectedProviders(t *testing.T) {
	h := newCapHarness(t)
	h.enable(models.ProviderBridge)
	h.enable(models.ProviderOzav)
	h.disableEveryUndeclaredProvider(models.ProviderBridge, models.ProviderOzav)
	h.alwaysResolves(nil)
	h.seedRows(
		row(models.ProviderBridge, models.KycStatusApproved),
		row(models.ProviderOzav, models.KycStatusRejected),
	)

	view, err := h.service().Compute(context.Background(), 1, 1)
	require.NoError(t, err)
	require.Empty(t, view.PendingProviders)
}

// The banner this list feeds reads "there are still steps with a partner;
// finish when you like", and a tap opens that partner's wizard. A partner that
// is only reviewing owes the user nothing: listed, it opened the wizard on its
// review page and invited a second submission to a provider already deciding
// (app smoke, 02/10). One still asking for a file stays.
func TestPendingProvidersLeavesOutAPartnerThatOnlyReviews(t *testing.T) {
	for _, tc := range []struct {
		name string
		ozav models.CustomerProviderKyc
		want []string
	}{
		{"under review", row(models.ProviderOzav, models.KycStatusUnderReview), nil},
		{"a submission in flight", row(models.ProviderOzav, models.KycStatusSubmitting), nil},
		{"incomplete, asking for a statement",
			row(models.ProviderOzav, models.KycStatusIncomplete, "bank_statement"), []string{models.ProviderOzav}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newCapHarness(t)
			h.enable(models.ProviderBridge)
			h.enable(models.ProviderOzav)
			h.disableEveryUndeclaredProvider(models.ProviderBridge, models.ProviderOzav)
			h.alwaysResolves(nil)
			h.seedRows(row(models.ProviderBridge, models.KycStatusApproved), tc.ozav)

			view, err := h.service().Compute(context.Background(), 1, 1)
			require.NoError(t, err)

			var got []string
			for _, p := range view.PendingProviders {
				got = append(got, p.Provider)
			}
			require.Equal(t, tc.want, got)
		})
	}
}

// TestComputePropagatesGenuineFailures proves a failure to even decide is
// never read as "the user is not eligible" — the read model must not tell a
// user their KYC is the reason when this side could not answer at all.
func TestComputePropagatesGenuineFailures(t *testing.T) {
	h := newCapHarness(t)
	h.enable(models.ProviderBridge)
	h.disableEveryUndeclaredProvider(models.ProviderBridge)
	boom := errors.New("settings store unreachable")
	h.alwaysResolves(boom)

	_, err := h.service().Compute(context.Background(), 1, 1)
	require.Error(t, err)
	require.ErrorIs(t, err, boom)
}

// A flag-store outage is the honest "not serving", never a KYC reason.
func TestAnUnreadableRailSwitchReadsAsRailDisabled(t *testing.T) {
	h := newCapHarness(t)
	h.enable(models.ProviderBridge)
	h.disableEveryUndeclaredProvider(models.ProviderBridge)
	unreadable := mocks.NewMockFeature(t)
	unreadable.EXPECT().IsActive(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(false, errors.New("featureflag: redis: connection refused"))
	h.features = unreadable
	h.seedRows(row(models.ProviderBridge, models.KycStatusApproved))

	view, err := h.service().Compute(context.Background(), 1, 1)
	require.NoError(t, err)
	require.False(t, view.Usable)
	for _, cap := range view.Capabilities {
		require.Equal(t, "rail_disabled", cap.BlockedBy.Reason, "%+v", cap)
	}
	require.Empty(t, view.PendingProviders)
}

// The rail switches are the operator's, read for the user, as every operation
// reads them; what is open is the customer's (spec §3.2): user 3 operating
// customer 1 is held by user 3's switch, and resolved for customer 1.
func TestCapabilitiesReadTheSwitchesOfTheUserAndTheRailsOfTheCustomer(t *testing.T) {
	h := newCapHarness(t)
	h.enable(models.ProviderBridge)
	h.disableEveryUndeclaredProvider(models.ProviderBridge)
	// Every rail but pix_payin is switched off for user 3, the USD ones too.
	for _, rail := range ramp.AllRails() {
		if rail != ramp.RailPixPayin {
			require.NoError(t, h.features.Deactivate(context.Background(), rail.Flag(), models.UserFlagScope(3)))
		}
	}
	h.seedRows(row(models.ProviderBridge, models.KycStatusApproved))
	h.resolver.EXPECT().
		Resolve(testifymock.Anything, uint(1), testifymock.MatchedBy(func(cap ramp.Capability) bool {
			return cap.Rail == ramp.RailPixPayin
		})).
		Return(nil, ramp.CustomerRef{}, nil)

	view, err := h.service().Compute(context.Background(), 3, 1)

	require.NoError(t, err)
	for _, cap := range view.Capabilities {
		require.Equal(t, cap.Rail == ramp.RailPixPayin.String(), cap.Available, "%+v", cap)
	}
}

// bankRows returns the ach_payout and wire_payout rows of a view.
func bankRows(view *kyc.CapabilitiesView) []kyc.CapabilityState {
	var out []kyc.CapabilityState
	for _, cap := range view.Capabilities {
		if ramp.Rail(cap.Rail).IsBank() {
			out = append(out, cap)
		}
	}
	return out
}

// The bank rails ship closed: they list as rail_disabled, never reach the
// resolver, and put nothing on the banner.
func TestBankRailsListClosedByTheirFlagAndStayOffTheBanner(t *testing.T) {
	h := newCapHarness(t)
	h.enable(models.ProviderBlindPay)
	h.disableEveryUndeclaredProvider(models.ProviderBlindPay)
	h.seedRows(row(models.ProviderBlindPay, models.KycStatusApproved))
	// Only Pix tuples may reach the resolver: the closed bank rails answer
	// before it, and a bank tuple arriving here fails the test.
	h.resolver.EXPECT().
		Resolve(testifymock.Anything, uint(1), testifymock.MatchedBy(func(cap ramp.Capability) bool {
			return !cap.Rail.IsBank()
		})).Return(nil, ramp.CustomerRef{}, nil)

	view, err := h.service().Compute(context.Background(), 1, 1)
	require.NoError(t, err)
	rows := bankRows(view)
	require.Len(t, rows, 6, "ach and wire x 3 tokens")
	for _, cap := range rows {
		require.Equal(t, "solana", cap.Chain)
		require.False(t, cap.Available, "%+v", cap)
		require.Equal(t, &kyc.BlockedBy{Reason: "rail_disabled"}, cap.BlockedBy)
	}
	require.Empty(t, view.PendingProviders)
}

// Open, with no BlindPay KYC finished: kyc_pending naming blindpay and the
// steps its bank_payout still owes.
func TestOpenBankRailsWithoutKycAreKycPendingNamingBlindPay(t *testing.T) {
	h := newCapHarness(t)
	h.enable(models.ProviderBlindPay)
	h.disableEveryUndeclaredProvider(models.ProviderBlindPay)
	h.openBank()
	h.seedRows(row(models.ProviderBlindPay, models.KycStatusIncomplete))
	h.steps.EXPECT().PendingSteps(uint(1), models.ProviderBlindPay).Return([]string{"bank_statement"}).Once()
	h.resolver.EXPECT().
		Resolve(testifymock.Anything, uint(1), testifymock.Anything).
		RunAndReturn(func(_ context.Context, _ uint, cap ramp.Capability) (ramp.ProviderAdapter, ramp.CustomerRef, error) {
			if cap.Rail.IsBank() {
				return nil, ramp.CustomerRef{}, &ramp.KycBlocked{
					Provider: models.ProviderBlindPay, Status: models.KycStatusIncomplete, Err: ramp.ErrKycPending,
				}
			}
			return nil, ramp.CustomerRef{}, nil
		})

	view, err := h.service().Compute(context.Background(), 1, 1)
	require.NoError(t, err)
	rows := bankRows(view)
	require.Len(t, rows, 6)
	for _, cap := range rows {
		require.False(t, cap.Available)
		require.Equal(t, &kyc.BlockedBy{
			Reason: "kyc_pending", Provider: models.ProviderBlindPay, PendingSteps: []string{"bank_statement"},
		}, cap.BlockedBy)
	}
	require.Len(t, view.PendingProviders, 1)
	require.Equal(t, models.ProviderBlindPay, view.PendingProviders[0].Provider)
}

// Open and approved with bank_payout: available, and usable.
func TestOpenBankRailsApprovedWithBankPayoutAreAvailable(t *testing.T) {
	h := newCapHarness(t)
	h.enable(models.ProviderBlindPay)
	h.disableEveryUndeclaredProvider(models.ProviderBlindPay)
	h.openBank()
	h.seedRows(row(models.ProviderBlindPay, models.KycStatusApproved))
	h.resolver.EXPECT().
		Resolve(testifymock.Anything, uint(1), testifymock.Anything).
		RunAndReturn(func(_ context.Context, _ uint, cap ramp.Capability) (ramp.ProviderAdapter, ramp.CustomerRef, error) {
			if cap.Rail.IsBank() {
				return nil, ramp.CustomerRef{}, nil
			}
			return nil, ramp.CustomerRef{}, ramp.ErrNoProviderAvailable
		})

	view, err := h.service().Compute(context.Background(), 1, 1)
	require.NoError(t, err)
	require.True(t, view.Usable)
	for _, cap := range bankRows(view) {
		require.True(t, cap.Available, "%+v", cap)
		require.Nil(t, cap.BlockedBy)
	}
}

// A Pix-only user: bank rows closed, so usable is exactly what the Pix rows
// say, both ways.
func TestBankRailsLeaveUsableOfAPixOnlyUserUnchanged(t *testing.T) {
	for _, pixOpen := range []bool{true, false} {
		h := newCapHarness(t)
		h.enable(models.ProviderBridge)
		h.disableEveryUndeclaredProvider(models.ProviderBridge)
		h.seedRows(row(models.ProviderBridge, models.KycStatusApproved))
		var pixErr error
		if !pixOpen {
			pixErr = ramp.ErrNoProviderAvailable
		}
		h.alwaysResolves(pixErr)

		view, err := h.service().Compute(context.Background(), 1, 1)
		require.NoError(t, err)
		require.Equal(t, pixOpen, view.Usable)
	}
}

// The declaration travels into the capability the resolver is asked about, so
// a Pix payout served only by a provider that prepares funding is listed
// exactly when the estimate would route to it.
func TestCapabilitiesListBlindPayPixPayoutOnlyWhenPreparedIsDeclared(t *testing.T) {
	for _, declared := range []bool{false, true} {
		t.Run(fmt.Sprintf("declared=%t", declared), func(t *testing.T) {
			h := newCapHarness(t)
			h.enable(models.ProviderBlindPay)
			h.disableEveryUndeclaredProvider(models.ProviderBlindPay)
			h.seedRows(row(models.ProviderBlindPay, models.KycStatusApproved))
			h.seedCustomers(models.ProviderBlindPay)
			h.resolver.EXPECT().
				Resolve(testifymock.Anything, uint(1), testifymock.Anything).
				RunAndReturn(func(_ context.Context, _ uint, cap ramp.Capability) (ramp.ProviderAdapter, ramp.CustomerRef, error) {
					if cap.Rail == ramp.RailPixPayout && !cap.PreparedFunding {
						return nil, ramp.CustomerRef{}, ramp.ErrNoProviderAvailable
					}
					if cap.Rail != ramp.RailPixPayout && cap.PreparedFunding != declared {
						t.Errorf("%s resolved with PreparedFunding=%t, declared %t", cap.Rail, cap.PreparedFunding, declared)
					}
					return nil, ramp.CustomerRef{}, nil
				})

			view, err := h.service().ComputeFor(context.Background(), 1, 1, declared)

			require.NoError(t, err)
			for _, state := range view.Capabilities {
				if state.Rail == ramp.RailPixPayout.String() {
					require.Equal(t, declared, state.Available, "%+v", state)
				}
			}
		})
	}
}
