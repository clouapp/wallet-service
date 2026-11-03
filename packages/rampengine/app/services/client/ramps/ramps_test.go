package ramps_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/goravel/framework/database/orm"
	"github.com/goravel/framework/support/carbon"
	"github.com/goravel/framework/testing/mock"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/featureflags/catalog"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/pixkeys"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/services/mfa"
	"github.com/macrowallets/waas/packages/rampengine/packages/featureflag"
	"github.com/macrowallets/waas/packages/rampengine/packages/featureflag/contracts"
	"github.com/macrowallets/waas/packages/rampengine/packages/featureflag/drivers"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
	"github.com/macrowallets/waas/packages/rampengine/tests/support"
	"github.com/macrowallets/waas/packages/rampengine/tests/support/race"
)

// openFeatures is the flag catalogue on an in-memory store, where every rail
// ships open. These tests are about routing, pricing and persistence — not
// about the flag, whose own behaviour is covered in tests/feature/api/client/ramps.
func openFeatures() contracts.Feature {
	m := featureflag.NewManager(drivers.NewMemory())
	catalog.Register(m)
	return m
}

// passthroughLocker runs fn immediately. Real lock semantics are covered by
// the AdvisoryLocker integration test.
type passthroughLocker struct{}

func (passthroughLocker) WithLock(ctx context.Context, _ string, fn func(context.Context) error) error {
	return fn(ctx)
}

const (
	userID     = uint(7)
	wallet     = "8nWZBsolanawallet"
	pixKey     = "39053344705"
	estimateID = "11111111-2222-3333-4444-555555555555"
)

// user is the account the controller resolves before a step-up call.
var user = &models.User{Model: orm.Model{ID: userID}}

// person is the user's individual customer, what a request without the
// customer header acts for.
var person = support.IndividualCustomer(userID)

// harness wires the router with mocked repositories so the eligibility chain —
// rail order, provider config, integration, endorsements — is exercised for
// real rather than stubbed out. That chain is what decides where money goes.
type harness struct {
	lastRail      string
	lastProviders []string
	fees          map[string]string
	// router, cfg and appEnv are kept so a test can rebuild one of the two
	// services with a different collaborator — the nil-port case, which no
	// setter on a built service could express honestly.
	router       *ramp.Router
	cfg          *ramp.Config
	appEnv       string
	deps         ramps.Deps
	onramp       *ramps.OnrampService
	offramp      *ramps.OfframpService
	transactions *ramps.TransactionService
	adapter      *mocks.MockProviderAdapter
	second       *mocks.MockProviderAdapter
	delivering   *deliveringAdapter
	settings     *mocks.MockSettingsSource
	integ        *mocks.MockCustomerProviderIntegrationRepository
	kycs         *mocks.MockCustomerProviderKycRepository
	profiles     *mocks.MockCustomerIndividualProfileRepository
	persons      ramp.Persons
	txRepo       *mocks.MockTransactionRepository
	vaRepo       *mocks.MockCustomerVirtualAccountRepository
	estimateRepo *mocks.MockRampEstimateRepository
	factory      *ramp.Factory
	mfa          *mocks.MockMfaStepUpValidator
	broadcaster  *mocks.MockBroadcaster
	notifier     *mocks.MockNotifier
	// announced is the order statuses the devices were told about, in order.
	announced    []string
	pendingSteps *mocks.MockPendingStepsSource
	// adapterServes / secondServes answer Supports for the two adapters. They
	// are fields rather than expectations because testify answers from the
	// FIRST matching one, so a second, narrower Supports registered by a test
	// would never be reached.
	adapterServes func(ramp.Capability) bool
	secondServes  func(ramp.Capability) bool
	// knownDestination / destinationLookupErr answer the off-ramp's lookup of
	// a destination an earlier order registered, and destinationLookups
	// records what was asked; destinationUpsertErr and upsertedDestinations do
	// the same for recording one. Fields for the same reason as the two above.
	destinations         *mocks.MockOfframpDestinationRepository
	knownDestination     string
	destinationLookupErr error
	destinationLookups   []models.OfframpDestination
	destinationUpsertErr error
	upsertedDestinations []models.OfframpDestination
	// knownDepositAddress answers the lookup of the deposit address an earlier
	// off-ramp got, and depositAddressLookups records the provider and token
	// asked. A field for the same reason as the ones above.
	knownDepositAddress   string
	depositAddressLookups []string
	// wallets answers the registry of the addresses customers hold as their
	// own, from registered (address → customer).
	wallets    *mocks.MockCustomerWalletRepository
	registered map[string]uint
	// businesses answers what each company declared: the company of
	// by_customer_test.go is ACME.
	businesses *mocks.MockCustomerBusinessProfileRepository
}

// servesEverything is the default capability answer: these tests are about
// pricing and persistence, not about the token vocabulary.
func servesEverything(ramp.Capability) bool { return true }

// noRetainedProfile is the profile repository of a user who retained none:
// the router narrows no provider by who they are, as before eligibility was
// per person. These tests are about pricing and persistence, not identity.
func noRetainedProfile(t *testing.T) *mocks.MockCustomerIndividualProfileRepository {
	profiles := mocks.NewMockCustomerIndividualProfileRepository(t)
	profiles.EXPECT().Find(testifymock.Anything).Return(nil, nil).Maybe()
	return profiles
}

// servesToken answers for a provider whose vocabulary is exactly one token. An
// unnamed token is a question the caller did not ask, so it is accepted.
func servesToken(token string) func(ramp.Capability) bool {
	return func(cap ramp.Capability) bool {
		return cap.Token == "" || strings.EqualFold(cap.Token, token)
	}
}

// railOrder configures which providers serve the rail, in order.
func newHarness(t *testing.T, rail string, providerCodes ...string) *harness {
	return newHarnessWithAppEnv(t, "local", rail, providerCodes...)
}

// deliveringAdapter is a ProviderAdapter that also satisfies the optional
// OnrampDeliveryAdapter port. It embeds the generated mock so every other
// method keeps its mocked behaviour, and adds the one method mockery cannot
// generate onto it — which is also exactly the shape the service type-asserts
// for, so the double proves the assertion rather than bypassing it.
type deliveringAdapter struct {
	*mocks.MockProviderAdapter
	delivered *ramp.OnrampDeliveryRequest
	order     *ramp.RampOrder
	err       error
}

func (d *deliveringAdapter) DeliverOnramp(
	_ context.Context, _ ramp.CustomerRef, req ramp.OnrampDeliveryRequest,
) (*ramp.RampOrder, error) {
	d.delivered = &req
	return d.order, d.err
}

func newHarnessWithAppEnv(t *testing.T, appEnv, rail string, providerCodes ...string) *harness {
	t.Helper()
	facades := mock.Factory()
	facades.Log()
	// The destination lookup hashes the Pix key with APP_KEY.
	facades.Config().EXPECT().GetString("app.key").Return("test-app-key").Maybe()

	h := &harness{
		adapter:      mocks.NewMockProviderAdapter(t),
		second:       mocks.NewMockProviderAdapter(t),
		settings:     mocks.NewMockSettingsSource(t),
		integ:        mocks.NewMockCustomerProviderIntegrationRepository(t),
		kycs:         mocks.NewMockCustomerProviderKycRepository(t),
		txRepo:       mocks.NewMockTransactionRepository(t),
		vaRepo:       mocks.NewMockCustomerVirtualAccountRepository(t),
		estimateRepo: mocks.NewMockRampEstimateRepository(t),
		mfa:          mocks.NewMockMfaStepUpValidator(t),
		broadcaster:  mocks.NewMockBroadcaster(t),
		notifier:     mocks.NewMockNotifier(t),
		pendingSteps: mocks.NewMockPendingStepsSource(t),
		destinations: mocks.NewMockOfframpDestinationRepository(t),
		wallets:      mocks.NewMockCustomerWalletRepository(t),
		registered:   map[string]uint{},
		businesses:   mocks.NewMockCustomerBusinessProfileRepository(t),
	}
	h.businesses.EXPECT().FindByCustomers(testifymock.Anything).
		RunAndReturn(func(ids []uint) (map[uint]models.BusinessProfile, error) {
			out := map[uint]models.BusinessProfile{}
			for _, id := range ids {
				out[id] = models.BusinessProfile{LegalName: companyLegalName, TradeName: "ACME"}
			}
			return out, nil
		}).Maybe()
	h.wallets.EXPECT().FindByAddress(testifymock.Anything, testifymock.Anything).
		RunAndReturn(func(chain, address string) (*models.CustomerWallet, error) {
			return registeredWallet(h.registered, chain, address), nil
		}).Maybe()
	h.notifier.EXPECT().TransactionUpdated(testifymock.Anything).Run(func(tx *models.Transaction) {
		h.announced = append(h.announced, tx.Status)
	}).Maybe()
	h.adapterServes = servesEverything
	h.secondServes = servesEverything
	h.adapter.EXPECT().Code().Return(models.ProviderBridge).Maybe()
	h.adapter.EXPECT().Supports(testifymock.Anything).
		RunAndReturn(func(cap ramp.Capability) bool { return h.adapterServes(cap) }).Maybe()
	h.second.EXPECT().Code().Return(models.ProviderOzav).Maybe()
	h.second.EXPECT().Supports(testifymock.Anything).
		RunAndReturn(func(cap ramp.Capability) bool { return h.secondServes(cap) }).Maybe()
	h.estimateRepo.EXPECT().FindByCustomerAndID(testifymock.Anything, testifymock.Anything).
		Return(nil, nil).Maybe()
	h.destinations.EXPECT().FindRef(testifymock.Anything).
		RunAndReturn(func(destination models.OfframpDestination) (string, error) {
			h.destinationLookups = append(h.destinationLookups, destination)
			return h.knownDestination, h.destinationLookupErr
		}).Maybe()
	h.destinations.EXPECT().Upsert(testifymock.Anything).
		RunAndReturn(func(destination models.OfframpDestination) error {
			h.upsertedDestinations = append(h.upsertedDestinations, destination)
			return h.destinationUpsertErr
		}).Maybe()

	h.txRepo.EXPECT().FindLatestOfframpDepositAddress(userID, testifymock.Anything, testifymock.Anything).
		RunAndReturn(func(_ uint, provider, token string) (string, error) {
			h.depositAddressLookups = append(h.depositAddressLookups, provider+"/"+token)
			return h.knownDepositAddress, nil
		}).Maybe()

	h.lastRail = rail
	h.lastProviders = providerCodes

	h.seedRail(rail, "", "", "", providerCodes...)
	h.stubOzavRailSettings(models.RailPixPayin)
	h.stubOzavRailSettings(models.RailPixPayout)

	for _, code := range providerCodes {
		group := ramp.ProviderGroup(code)
		h.settings.EXPECT().Bool(group, ramp.SettingEnabled).Return(true, nil).Maybe()
		h.settings.EXPECT().String(group, ramp.SettingMode).Return(models.ProviderModeSandbox, nil).Maybe()
		h.integ.EXPECT().FindByCustomerAndProvider(userID, code).Return(&models.CustomerProviderIntegration{
			CustomerID: userID, Provider: code, Status: models.IntegrationStatusActive,
			ExternalCustomerID: support.Ptr("cus_1"), Metadata: "{}",
		}, nil).Maybe()

		approved := &models.CustomerProviderKyc{
			CustomerID: userID, Provider: code, KycStatus: models.KycStatusApproved,
		}
		require.NoError(t, approved.SetEndorsements([]models.Endorsement{
			{Name: models.EndorsementPix, Status: models.EndorsementStatusApproved},
			{Name: models.EndorsementBankPayout, Status: models.EndorsementStatusApproved},
		}))
		h.kycs.EXPECT().FindByCustomerAndProvider(userID, code).Return(approved, nil).Maybe()
	}

	config := ramp.NewConfig(h.settings)
	// The Ozav slot is wrapped so a test can exercise the delivery port; the
	// factory keys on Code(), so the wrapper replaces it under the same code.
	h.delivering = &deliveringAdapter{MockProviderAdapter: h.second}
	h.factory = ramp.NewFactory(h.adapter, h.delivering)
	h.profiles = noRetainedProfile(t)
	h.persons = ramp.Persons{Customers: support.IndividualCustomers(t), Profiles: h.profiles}
	router := ramp.NewRouter(ramp.RouterDeps{
		Factory: h.factory, Config: config,
		Integrations: h.integ, Kyc: h.kycs, Persons: h.persons, AppEnv: appEnv,
	})
	h.router, h.cfg, h.appEnv = router, config, appEnv
	h.deps = ramps.Deps{
		Router: router, Config: config,
		Transactions: h.txRepo, Estimates: h.estimateRepo,
		Locker: passthroughLocker{}, Broadcaster: h.broadcaster, Members: support.IndividualMembers(t), Notifier: h.notifier,
		Features: openFeatures(), PendingSteps: h.pendingSteps,
		VirtualAccounts: h.vaRepo, Integrations: h.integ, Factory: h.factory, Wallets: h.wallets,
		Businesses: h.businesses,
	}
	h.onramp = ramps.NewOnrampService(h.deps)
	h.offramp = ramps.NewOfframpService(h.deps, h.destinations, h.mfa, appEnv, nil)
	readOnly := h.deps
	readOnly.PendingSteps = nil
	h.transactions = ramps.NewTransactionService(readOnly)
	return h
}

// seedRail stubs the four keys a rail group holds. Empty min/max mean
// unbounded, which is a blank stored value rather than a zero.
func (h *harness) seedRail(rail, min, max, minByToken string, providerCodes ...string) {
	group := ramp.RailGroup(ramp.Rail(rail))
	h.settings.EXPECT().String(group, ramp.SettingProviderOrder).
		Return(ramp.FormatProviderOrder(providerCodes), nil).Maybe()
	for _, code := range ramp.FeeProviderCodes() {
		h.settings.EXPECT().String(group, ramp.FeeSettingKey(code)).
			RunAndReturn(func(string, string) (string, error) { return h.feeFor(code), nil }).Maybe()
	}
	h.settings.EXPECT().String(group, ramp.SettingMinAmountBRL).Return(min, nil).Maybe()
	h.settings.EXPECT().String(group, ramp.SettingMaxAmountBRL).Return(max, nil).Maybe()
	h.settings.EXPECT().String(group, ramp.SettingMinAmountBRLByToken).Return(minByToken, nil).Maybe()
	switch ramp.Rail(rail) {
	case ramp.RailPixPayin:
		h.settings.EXPECT().String(group, ramp.SettingOzavDeliveryMaxAttempts).Return("3", nil).Maybe()
	case ramp.RailPixPayout:
		h.settings.EXPECT().String(group, ramp.SettingOzavOfframpFundingWindowMinutes).Return("30", nil).Maybe()
		h.settings.EXPECT().String(group, ramp.SettingOzavDepositMatchTolerance).Return("0", nil).Maybe()
	}
}

func (h *harness) stubOzavRailSettings(rail string) {
	group := ramp.RailGroup(ramp.Rail(rail))
	switch ramp.Rail(rail) {
	case ramp.RailPixPayout:
		h.settings.EXPECT().String(group, ramp.SettingOzavOfframpFundingWindowMinutes).Return("30", nil).Maybe()
		h.settings.EXPECT().String(group, ramp.SettingOzavDepositMatchTolerance).Return("0", nil).Maybe()
	case ramp.RailPixPayin:
		h.settings.EXPECT().String(group, ramp.SettingOzavDeliveryMaxAttempts).Return("3", nil).Maybe()
	}
}

// withFees sets the per-provider fees the rail answers from now on. It is
// read at call time, so it can change between an estimate and a create.
func (h *harness) withFees(fees map[string]string) { h.fees = fees }

// feeFor is a provider's fee on the seeded rail: 1.50 unless a test set one.
func (h *harness) feeFor(code string) string {
	if fee, ok := h.fees[code]; ok {
		return fee
	}
	return "1.50"
}

// config re-stubs one provider's enabled/mode flags after clearing settings
// expectations. Rail stubs are preserved so routing still works.
func (h *harness) config(code string, enabled bool, mode string) {
	h.settings.ExpectedCalls = nil
	h.seedRail(h.lastRail, "", "", "", h.lastProviders...)
	h.stubOzavRailSettings(models.RailPixPayin)
	h.stubOzavRailSettings(models.RailPixPayout)
	group := ramp.ProviderGroup(code)
	h.settings.EXPECT().Bool(group, ramp.SettingEnabled).Return(enabled, nil).Maybe()
	h.settings.EXPECT().String(group, ramp.SettingMode).Return(mode, nil).Maybe()
}

// withoutPendingSteps rebuilds the off-ramp service with no steps source at
// all. The port is optional by contract, and "optional" has to mean a refusal
// that arrives without steps rather than a panic on the one path a blocked
// user is guaranteed to take.
func (h *harness) withoutPendingSteps() {
	d := h.deps
	d.PendingSteps = nil
	h.offramp = ramps.NewOfframpService(d, h.destinations, h.mfa, h.appEnv, nil)
}

// withDepositAdapter rebuilds the factory so the harness's seeded provider
// also serves standing accounts, then rewires the transaction service the
// deposit-refresh tests exercise — the same pattern withoutPendingSteps uses
// to swap in a different collaborator. The router is left alone:
// refreshDepositOrder never routes (see ResolveDepositAdapter), so only the
// factory needs the new port.
func (h *harness) withDepositAdapter(t *testing.T) *mocks.MockVirtualAccountAdapter {
	t.Helper()
	standing := &standingAdapter{
		MockProviderAdapter:       h.adapter,
		MockVirtualAccountAdapter: mocks.NewMockVirtualAccountAdapter(t),
	}
	h.factory = ramp.NewFactory(standing, h.delivering)
	d := h.deps
	d.Factory = h.factory
	h.transactions = ramps.NewTransactionService(d)
	return standing.MockVirtualAccountAdapter
}

// blockKyc rewrites the KYC rows so one provider no longer clears the rail.
// The whole expectation set is replaced because testify answers from the first
// match: layering a second row for the same provider would never be reached.
func (h *harness) blockKyc(t *testing.T, blocked, status string, others ...string) {
	t.Helper()
	h.kycs.ExpectedCalls = nil
	h.kycs.EXPECT().FindByCustomerAndProvider(userID, blocked).Return(&models.CustomerProviderKyc{
		CustomerID: userID, Provider: blocked, KycStatus: status,
	}, nil).Maybe()
	for _, code := range others {
		approved := &models.CustomerProviderKyc{
			CustomerID: userID, Provider: code, KycStatus: models.KycStatusApproved,
		}
		require.NoError(t, approved.SetEndorsements([]models.Endorsement{
			{Name: models.EndorsementPix, Status: models.EndorsementStatusApproved},
		}))
		h.kycs.EXPECT().FindByCustomerAndProvider(userID, code).Return(approved, nil).Maybe()
	}
}

// withLimits replaces the rail setting with one that has bounds.
func (h *harness) withLimits(t *testing.T, rail, min, max string, providerCodes ...string) {
	t.Helper()
	h.settings.ExpectedCalls = nil
	h.seedRail(rail, min, max, "", providerCodes...)
	h.stubOzavRailSettings(models.RailPixPayin)
	h.stubOzavRailSettings(models.RailPixPayout)
	for _, code := range providerCodes {
		group := ramp.ProviderGroup(code)
		h.settings.EXPECT().Bool(group, ramp.SettingEnabled).Return(true, nil).Maybe()
		h.settings.EXPECT().String(group, ramp.SettingMode).Return(models.ProviderModeSandbox, nil).Maybe()
	}
}

func onrampInput() dto.EstimateOnrampDTO {
	return dto.EstimateOnrampDTO{AmountBRL: "100.00", Token: "usdc", Chain: "solana", Wallet: wallet}
}

func offrampInput() dto.EstimateOfframpDTO {
	return dto.EstimateOfframpDTO{
		AmountToken: "18.500000", Token: "usdc", Chain: "solana",
		FromAddress: wallet, PixKey: pixKey, PixKeyType: "cpf",
	}
}

func bridgeEstimate() *ramp.Estimate {
	return &ramp.Estimate{
		Binding: false, Rate: "0.185000", FeePercent: "1.50",
		FeeAmount: "1.50", FeeCurrency: "brl", EstimatedReceive: "18.222500",
	}
}

func storedEstimate(rail string) *models.RampEstimate {
	return &models.RampEstimate{
		Model: orm.Model{ID: 42}, ExternalID: estimateID, CustomerID: userID, UserID: userID,
		Provider: models.ProviderBridge, Rail: rail, FeePercent: "1.50",
		RawRequest: "{}",
		RawResponse: `{"rail":"` + rail + `","source_amount":"100.00","estimated_receive":"18.222500",` +
			`"rate":"0.185000","fee_percent":"1.50","fee_amount":"1.50","fee_currency":"brl"}`,
	}
}

func storedOfframpEstimate() *models.RampEstimate {
	return &models.RampEstimate{
		Model: orm.Model{ID: 42}, ExternalID: estimateID, CustomerID: userID, UserID: userID,
		Provider: models.ProviderBridge, Rail: models.RailPixPayout, FeePercent: "1.50",
		RawRequest: "{}",
		RawResponse: `{"rail":"` + models.RailPixPayout + `","source_amount":"18.500000","source_currency":"usdc",` +
			`"destination_currency":"brl","destination_payment_rail":"pix",` +
			`"pix_key":"` + pixKey + `","pix_key_type":"cpf","chain":"solana","from_address":"` + wallet + `",` +
			`"estimated_receive":"18.222500","rate":"0.185000","fee_percent":"1.50",` +
			`"fee_amount":"1.50","fee_currency":"brl"}`,
	}
}

// --- estimates --------------------------------------------------------------

func TestEstimateOnrampPersistsTheQuote(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderBridge)

	h.adapter.EXPECT().EstimateOnramp(testifymock.Anything, testifymock.Anything,
		testifymock.MatchedBy(func(req ramp.OnrampQuoteRequest) bool {
			// The rail's fee reaches the adapter; the client never sets it.
			return req.FeePercent == "1.50" && req.Source.Amount == "100.00"
		})).Return(bridgeEstimate(), nil).Once()

	var stored *models.RampEstimate
	h.estimateRepo.EXPECT().Create(testifymock.Anything).Return(nil).Once().
		Run(func(args testifymock.Arguments) { stored = args.Get(0).(*models.RampEstimate) })

	view, err := h.onramp.Estimate(context.Background(), user, person, onrampInput())
	require.NoError(t, err)

	require.Equal(t, models.ProviderBridge, stored.Provider, "the quote records who served it, so create can be sticky")
	require.Equal(t, "1.50", stored.FeePercent)
	require.NotEmpty(t, stored.ExternalID)
	require.Equal(t, &ramps.EstimateView{
		EstimateID: stored.ExternalID, Rail: models.RailPixPayin,
		Rate: "0.185000", FeePercent: "1.50", FeeAmount: "1.50", FeeCurrency: "brl",
		SourceAmount: "100.00", EstimatedReceive: "18.222500",
	}, view)
}

// The view answers the quote the provider gave, with the rail's fee rather than
// whatever fee the provider echoed, and a binding quote carries its expiry.
func TestEstimateOfframpAnswersTheQuoteItStored(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	expires := time.Now().Add(time.Minute)
	h.adapter.EXPECT().EstimateOfframp(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(&ramp.Estimate{
			Binding: true, ExpiresAt: &expires, ProviderQuoteID: "q_1",
			Rate: "5.405405", FeePercent: "9.99", FeeAmount: "0.277500",
			FeeCurrency: "usdc", EstimatedReceive: "98.50",
		}, nil).Once()
	var stored *models.RampEstimate
	h.estimateRepo.EXPECT().Create(testifymock.Anything).Return(nil).Once().
		Run(func(args testifymock.Arguments) { stored = args.Get(0).(*models.RampEstimate) })

	view, err := h.offramp.Estimate(context.Background(), user, person, offrampInput())

	require.NoError(t, err)
	require.Equal(t, "q_1", *stored.ProviderQuoteID)
	require.Equal(t, &ramps.EstimateView{
		EstimateID: stored.ExternalID, Rail: models.RailPixPayout,
		Rate: "5.405405", FeePercent: "1.50", FeeAmount: "0.277500", FeeCurrency: "usdc",
		SourceAmount: "18.500000", EstimatedReceive: "98.50",
		Binding: true, ExpiresAt: &expires, DepositMode: ramps.DepositModeSimulated,
		// A Pix payout lands in reais, and the view now says so.
		DestinationCurrency: "brl",
	}, view)
}

func TestEstimateOnrampUsesPerTokenMinimumOverride(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderOzav)
	h.settings.ExpectedCalls = nil
	h.seedRail(models.RailPixPayin, "400.00", "", `{"BRS":"10.00","USDC":"400.00"}`, models.ProviderOzav)
	group := ramp.ProviderGroup(models.ProviderOzav)
	h.settings.EXPECT().Bool(group, ramp.SettingEnabled).Return(true, nil).Maybe()
	h.settings.EXPECT().String(group, ramp.SettingMode).Return(models.ProviderModeSandbox, nil).Maybe()

	in := onrampInput()
	in.Token = models.TargetTokenBRS
	in.AmountBRL = "10.00"
	h.second.EXPECT().EstimateOnramp(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(bridgeEstimate(), nil).Once()
	h.estimateRepo.EXPECT().Create(testifymock.Anything).Return(nil).Once()

	_, err := h.onramp.Estimate(context.Background(), user, person, in)
	require.NoError(t, err)

	in.AmountBRL = "50.00"
	in.Token = models.TargetTokenUSDC
	_, err = h.onramp.Estimate(context.Background(), user, person, in)
	require.ErrorIs(t, err, ramp.ErrAmountBelowMinimum)
	h.second.AssertNumberOfCalls(t, "EstimateOnramp", 1)
}

func TestEstimateOfframpCarriesTheProviderDeduction(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	h.adapter.EXPECT().EstimateOfframp(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(&ramp.Estimate{
			Rate: "5.439000", FeePercent: "1.50", FeeAmount: "0.277500", FeeCurrency: "usdc",
			EstimatedReceive:         "98.50",
			ProviderDeductedAmount:   "0.800000",
			ProviderDeductedCurrency: "usdc",
		}, nil).Once()
	var stored *models.RampEstimate
	h.estimateRepo.EXPECT().Create(testifymock.Anything).Return(nil).Once().
		Run(func(args testifymock.Arguments) { stored = args.Get(0).(*models.RampEstimate) })

	view, err := h.offramp.Estimate(context.Background(), user, person, offrampInput())

	require.NoError(t, err)
	require.Equal(t, "0.800000", view.ProviderDeductedAmount)
	require.Equal(t, "usdc", view.ProviderDeductedCurrency)
	require.Equal(t, "98.50", view.EstimatedReceive, "the provider's net figure, untouched")
	require.Contains(t, stored.RawResponse, `"provider_deducted_amount":"0.800000"`)
	require.Contains(t, stored.RawResponse, `"provider_deducted_currency":"usdc"`)
}

// The quote names the provider's conversion gap beside XIP's fee, and keeps it
// with the quote: the app shows "Câmbio do parceiro 0,55% · R$0,29" instead of a
// lone 0% fee over a payout that lands 0.55% under the market.
func TestEstimateOfframpNamesTheProvidersConversionGap(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	h.adapter.EXPECT().EstimateOfframp(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(&ramp.Estimate{
			Rate: "5.182141", FeeAmount: "0.000000", FeeCurrency: "usdc", EstimatedReceive: "51.82",
			ProviderFeePercent: "0.55", ProviderFeeAmount: "0.29",
		}, nil).Once()
	var stored *models.RampEstimate
	h.estimateRepo.EXPECT().Create(testifymock.Anything).Return(nil).Once().
		Run(func(args testifymock.Arguments) { stored = args.Get(0).(*models.RampEstimate) })

	view, err := h.offramp.Estimate(context.Background(), user, person, offrampInput())

	require.NoError(t, err)
	require.Equal(t, "0.55", view.ProviderFeePercent)
	require.Equal(t, "0.29", view.ProviderFeeAmount)
	require.Contains(t, stored.RawResponse, `"provider_fee_percent":"0.55"`)
	require.Contains(t, stored.RawResponse, `"provider_fee_amount":"0.29"`)
}

func TestEstimateOnrampCarriesTheProviderDeduction(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderBridge)
	h.adapter.EXPECT().EstimateOnramp(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(&ramp.Estimate{
			Rate: "0.185000", FeePercent: "1.50", FeeAmount: "1.50", FeeCurrency: "brl",
			EstimatedReceive:         "18.222500",
			ProviderDeductedAmount:   "3.50",
			ProviderDeductedCurrency: "brl",
		}, nil).Once()
	var stored *models.RampEstimate
	h.estimateRepo.EXPECT().Create(testifymock.Anything).Return(nil).Once().
		Run(func(args testifymock.Arguments) { stored = args.Get(0).(*models.RampEstimate) })

	view, err := h.onramp.Estimate(context.Background(), user, person, onrampInput())

	require.NoError(t, err)
	require.Equal(t, "3.50", view.ProviderDeductedAmount)
	require.Equal(t, "brl", view.ProviderDeductedCurrency)
	require.Equal(t, "18.222500", view.EstimatedReceive)
	require.Contains(t, stored.RawResponse, `"provider_deducted_amount":"3.50"`)
	require.Contains(t, stored.RawResponse, `"provider_deducted_currency":"brl"`)
}

func TestEstimateWithoutAProviderDeductionStoresNoDeductionKeys(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderBridge)
	h.adapter.EXPECT().EstimateOnramp(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(bridgeEstimate(), nil).Once()
	var stored *models.RampEstimate
	h.estimateRepo.EXPECT().Create(testifymock.Anything).Return(nil).Once().
		Run(func(args testifymock.Arguments) { stored = args.Get(0).(*models.RampEstimate) })

	view, err := h.onramp.Estimate(context.Background(), user, person, onrampInput())

	require.NoError(t, err)
	require.Empty(t, view.ProviderDeductedAmount)
	require.Empty(t, view.ProviderDeductedCurrency)
	require.NotContains(t, stored.RawResponse, "provider_deducted")
}

// The estimate names the deposit mode of the provider that quoted it, which is
// what the order will be placed with: the apps decide on the quote whether
// the wallet needs SOL to send the crypto.
func TestEstimateOfframpSaysOnChainForAProductionProvider(t *testing.T) {
	h := newHarnessWithAppEnv(t, "production", models.RailPixPayout, models.ProviderBridge)
	h.config(models.ProviderBridge, true, models.ProviderModeProduction)
	h.adapter.EXPECT().EstimateOfframp(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(&ramp.Estimate{Rate: "5.405405", FeeAmount: "0.277500", FeeCurrency: "usdc", EstimatedReceive: "98.50"}, nil).Once()
	h.estimateRepo.EXPECT().Create(testifymock.Anything).Return(nil).Once()

	view, err := h.offramp.Estimate(context.Background(), user, person, offrampInput())

	require.NoError(t, err)
	require.Equal(t, ramps.DepositModeOnChain, view.DepositMode)
}

// The provider gives a customer one deposit address. Naming the one an earlier
// order got lets the app tell, before placing the order, whether the deposit
// also creates the address's token account, whose rent the sender pays.
func TestEstimateOfframpNamesTheDepositAddressTheProviderAlreadyGave(t *testing.T) {
	h := newHarnessWithAppEnv(t, "production", models.RailPixPayout, models.ProviderBridge)
	h.config(models.ProviderBridge, true, models.ProviderModeProduction)
	h.knownDepositAddress = "BridgeDepositAddr"
	h.adapter.EXPECT().EstimateOfframp(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(&ramp.Estimate{Rate: "5.405405", FeeAmount: "0.277500", FeeCurrency: "usdc", EstimatedReceive: "98.50"}, nil).Once()
	h.estimateRepo.EXPECT().Create(testifymock.Anything).Return(nil).Once()

	view, err := h.offramp.Estimate(context.Background(), user, person, offrampInput())

	require.NoError(t, err)
	require.Equal(t, "BridgeDepositAddr", view.DepositToAddress)
	require.Equal(t, []string{models.ProviderBridge + "/usdc"}, h.depositAddressLookups)
}

// Before the first order there is no address to name, and the app reads that
// as a deposit that creates the token account.
func TestEstimateOfframpNamesNoDepositAddressBeforeTheFirstOrder(t *testing.T) {
	h := newHarnessWithAppEnv(t, "production", models.RailPixPayout, models.ProviderBridge)
	h.config(models.ProviderBridge, true, models.ProviderModeProduction)
	h.adapter.EXPECT().EstimateOfframp(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(&ramp.Estimate{Rate: "5.405405", FeeAmount: "0.277500", FeeCurrency: "usdc", EstimatedReceive: "98.50"}, nil).Once()
	h.estimateRepo.EXPECT().Create(testifymock.Anything).Return(nil).Once()

	view, err := h.offramp.Estimate(context.Background(), user, person, offrampInput())

	require.NoError(t, err)
	require.Empty(t, view.DepositToAddress)
}

// A simulated deposit sends nothing on chain: there is no address to name.
func TestEstimateOfframpNamesNoDepositAddressForASimulatedDeposit(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	h.knownDepositAddress = "BridgeDepositAddr"
	h.adapter.EXPECT().EstimateOfframp(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(&ramp.Estimate{Rate: "5.405405", FeeAmount: "0.277500", FeeCurrency: "usdc", EstimatedReceive: "98.50"}, nil).Once()
	h.estimateRepo.EXPECT().Create(testifymock.Anything).Return(nil).Once()

	view, err := h.offramp.Estimate(context.Background(), user, person, offrampInput())

	require.NoError(t, err)
	require.Equal(t, ramps.DepositModeSimulated, view.DepositMode)
	require.Empty(t, view.DepositToAddress)
	require.Empty(t, h.depositAddressLookups)
}

func TestEstimateOnrampRejectsAmountBelowMinimum(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderBridge)
	h.withLimits(t, models.RailPixPayin, "50.00", "", models.ProviderBridge)

	in := onrampInput()
	in.AmountBRL = "10.00"

	_, err := h.onramp.Estimate(context.Background(), user, person, in)
	require.ErrorIs(t, err, ramp.ErrAmountBelowMinimum)
	h.adapter.AssertNotCalled(t, "EstimateOnramp",
		testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

func TestEstimateOnrampRejectsAmountAboveMaximum(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderBridge)
	h.withLimits(t, models.RailPixPayin, "", "50.00", models.ProviderBridge)

	_, err := h.onramp.Estimate(context.Background(), user, person, onrampInput())
	require.ErrorIs(t, err, ramp.ErrAmountAboveMaximum)
}

// The refusal carries the bound it refused against, so the client can name the
// number instead of sending the user back to guess it. errors.Is has to keep
// working through the type, because the error-code table matches sentinels.
func TestLimitRefusalCarriesTheBound(t *testing.T) {
	cases := []struct {
		name         string
		min, max     string
		amountBRL    string
		wantSentinel error
		wantLimitBRL string
	}{
		{"below the minimum", "50.00", "", "10.00", ramp.ErrAmountBelowMinimum, "50.00"},
		{"above the maximum", "", "50.00", "100.00", ramp.ErrAmountAboveMaximum, "50.00"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, models.RailPixPayin, models.ProviderBridge)
			h.withLimits(t, models.RailPixPayin, c.min, c.max, models.ProviderBridge)

			in := onrampInput()
			in.AmountBRL = c.amountBRL

			_, err := h.onramp.Estimate(context.Background(), user, person, in)
			require.ErrorIs(t, err, c.wantSentinel)

			var violation *ramp.LimitViolation
			require.ErrorAs(t, err, &violation)
			require.Equal(t, c.wantLimitBRL, violation.LimitBRL)
			// The on-ramp checks before a quote exists, so there is nothing to
			// convert the bound with.
			require.Empty(t, violation.LimitToken)
		})
	}
}

// The off-ramp's BRL leg only exists once the quote does, so the limit is
// enforced against what the user would actually receive.
func TestEstimateOfframpChecksLimitsAgainstTheQuotedBRL(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	h.withLimits(t, models.RailPixPayout, "500.00", "", models.ProviderBridge)

	h.adapter.EXPECT().EstimateOfframp(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(&ramp.Estimate{
			Rate: "5.405405", FeePercent: "1.50", FeeAmount: "0.277500",
			FeeCurrency: "usdc", EstimatedReceive: "98.50",
		}, nil).Once()

	_, err := h.offramp.Estimate(context.Background(), user, person, offrampInput())
	require.ErrorIs(t, err, ramp.ErrAmountBelowMinimum)
	h.estimateRepo.AssertNotCalled(t, "Create", testifymock.Anything)
}

// The off-ramp form is denominated in a token while the rail's bound is BRL,
// so the refusal carries the bound converted on the quote that was just made.
func TestOfframpLimitRefusalCarriesTheBoundInTokens(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	h.withLimits(t, models.RailPixPayout, "20.00", "", models.ProviderBridge)

	// 2 tokens less a 1.5% fee quote 10.78 BRL. The R$20 floor is 3.7122… of
	// them: a bare rate division would say 3.66 and half-up 3.71, both short.
	h.adapter.EXPECT().EstimateOfframp(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(&ramp.Estimate{
			Rate: "5.470000", FeePercent: "1.50", FeeAmount: "0.030000",
			FeeCurrency: "usdc", EstimatedReceive: "10.78",
		}, nil).Once()

	in := offrampInput()
	in.AmountToken = "2"

	_, err := h.offramp.Estimate(context.Background(), user, person, in)

	require.ErrorIs(t, err, ramp.ErrAmountBelowMinimum)
	var violation *ramp.LimitViolation
	require.ErrorAs(t, err, &violation)
	require.Equal(t, "20.00", violation.LimitBRL)
	require.Equal(t, "3.72", violation.LimitToken)
}

// The cap is the minimum's mirror, and rounding it the same way suggests an
// amount over the cap — the same loop, seen from the other side.
func TestOfframpLimitRefusalAboveMaximumSuggestsAnAmountUnderIt(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	h.withLimits(t, models.RailPixPayout, "", "50.00", models.ProviderBridge)

	h.adapter.EXPECT().EstimateOfframp(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(&ramp.Estimate{
			Rate: "5.470000", FeePercent: "0", FeeAmount: "0",
			FeeCurrency: "usdc", EstimatedReceive: "54.70",
		}, nil).Once()

	in := offrampInput()
	in.AmountToken = "10"

	_, err := h.offramp.Estimate(context.Background(), user, person, in)

	require.ErrorIs(t, err, ramp.ErrAmountAboveMaximum)
	var violation *ramp.LimitViolation
	require.ErrorAs(t, err, &violation)
	require.Equal(t, "50.00", violation.LimitBRL)
	// 10 × 50 / 54.705 = 9.1399…, floored. 9.14 would re-quote to exactly the
	// cap; 9.13 is the largest amount that stays under it on a quote whose
	// receive may really be 54.695.
	require.Equal(t, "9.13", violation.LimitToken)
}

// A quote that produced nothing carries no proportion to convert with. The
// refusal still has to reach the user, just without the token figure.
func TestOfframpLimitRefusalSurvivesAnUnusableQuote(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	h.withLimits(t, models.RailPixPayout, "20.00", "", models.ProviderBridge)

	h.adapter.EXPECT().EstimateOfframp(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(&ramp.Estimate{
			Rate: "5.470000", FeePercent: "0", FeeAmount: "0",
			FeeCurrency: "usdc", EstimatedReceive: "0.00",
		}, nil).Once()

	_, err := h.offramp.Estimate(context.Background(), user, person, offrampInput())

	require.ErrorIs(t, err, ramp.ErrAmountBelowMinimum)
	var violation *ramp.LimitViolation
	require.ErrorAs(t, err, &violation)
	require.Equal(t, "20.00", violation.LimitBRL)
	require.Empty(t, violation.LimitToken)
}

// --- capability routing and the refusal that names a provider ---------------

// The estimate routes on the whole capability, not on the rail alone. A
// provider that cannot price the token the user asked for is not a candidate
// for it, however approved the user is there.
func TestEstimateOfframpRoutesOnTheTokenTheUserAsked(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge, models.ProviderOzav)
	h.adapterServes = servesToken("USDC")
	h.secondServes = servesToken("USDT")

	h.second.EXPECT().EstimateOfframp(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(bridgeEstimate(), nil).Once()
	var stored *models.RampEstimate
	h.estimateRepo.EXPECT().Create(testifymock.Anything).Return(nil).Once().
		Run(func(args testifymock.Arguments) { stored = args.Get(0).(*models.RampEstimate) })

	in := offrampInput()
	in.Token = "usdt"

	_, err := h.offramp.Estimate(context.Background(), user, person, in)
	require.NoError(t, err)
	require.Equal(t, models.ProviderOzav, stored.Provider,
		"the head of the rail order cannot price USDT; the router must walk past it")
	h.adapter.AssertNotCalled(t, "EstimateOfframp",
		testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// The steps come from the port, not from the router: the ramp package must not
// import kyc, and the dependency only runs the other way.
func TestEstimateOfframpEnrichesAKycRefusalWithThatProvidersSteps(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderOzav)
	h.blockKyc(t, models.ProviderOzav, models.KycStatusIncomplete)
	h.pendingSteps.EXPECT().PendingSteps(userID, models.ProviderOzav).
		Return([]string{"profile", "proof_of_address"}).Once()

	_, err := h.offramp.Estimate(context.Background(), user, person, offrampInput())

	var blocked *ramp.KycBlocked
	require.ErrorAs(t, err, &blocked)
	require.ErrorIs(t, err, ramp.ErrKycPending)
	require.Equal(t, models.ProviderOzav, blocked.Provider)
	require.Equal(t, []string{"profile", "proof_of_address"}, blocked.PendingSteps)
}

// A provider that refused in definitive gets no wizard: the code says so, and
// nothing is asked of the port, because there is no step left to name.
func TestEstimateOfframpReportsAKycRejectionWithoutAskingForSteps(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderOzav)
	h.blockKyc(t, models.ProviderOzav, models.KycStatusRejected)

	_, err := h.offramp.Estimate(context.Background(), user, person, offrampInput())

	var blocked *ramp.KycBlocked
	require.ErrorAs(t, err, &blocked)
	require.ErrorIs(t, err, ramp.ErrKycRejected)
	require.Equal(t, models.ProviderOzav, blocked.Provider)
	require.Empty(t, blocked.PendingSteps)
	h.pendingSteps.AssertNotCalled(t, "PendingSteps", testifymock.Anything, testifymock.Anything)
}

// A deployment that never wired the port still refuses honestly. The steps are
// a convenience for the screen; the refusal is the answer, and swapping a 409
// the user can act on for a panic would be the worse failure by far.
func TestEstimateOfframpSurvivesAKycRefusalWithNoPendingStepsSource(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderOzav)
	h.blockKyc(t, models.ProviderOzav, models.KycStatusIncomplete)
	h.withoutPendingSteps()

	var err error
	require.NotPanics(t, func() {
		_, err = h.offramp.Estimate(context.Background(), user, person, offrampInput())
	})

	var blocked *ramp.KycBlocked
	require.ErrorAs(t, err, &blocked)
	require.Equal(t, models.ProviderOzav, blocked.Provider)
	require.Empty(t, blocked.PendingSteps)
}

// --- create: estimate lifecycle --------------------------------------------
//
// These exercise the engine both directions share — loadEstimate, dedup, the
// reservation row, releaseReservation. They run against the off-ramp because it
// is the only direction that still creates an order: the on-ramp collects
// through a standing deposit account, so there is nothing for a client to
// create there.

func TestCreateOfframpRejectsExpiredEstimate(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	h.mfa.EXPECT().Enrolled(testifymock.Anything).Return(false, nil).Once()

	expired := storedOfframpEstimate()
	past := time.Now().Add(-time.Minute)
	expired.ExpiresAt = &past
	h.estimateRepo.EXPECT().FindByCustomerAndExternalID(userID, estimateID).Return(expired, nil).Once()

	_, _, err := h.offramp.Create(context.Background(), user, person,
		dto.CreateOfframpDTO{EstimateOfframpDTO: offrampInput(), EstimateID: estimateID})
	require.ErrorIs(t, err, ramp.ErrEstimateExpired)
	h.txRepo.AssertNotCalled(t, "Create", testifymock.Anything)
}

// A binding quote is single-use. Without this the same locked price creates
// two orders.
func TestCreateOfframpRejectsConsumedEstimate(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	h.mfa.EXPECT().Enrolled(testifymock.Anything).Return(false, nil).Once()

	consumed := storedOfframpEstimate()
	now := time.Now()
	consumed.ConsumedAt = &now
	h.estimateRepo.EXPECT().FindByCustomerAndExternalID(userID, estimateID).Return(consumed, nil).Once()

	_, _, err := h.offramp.Create(context.Background(), user, person,
		dto.CreateOfframpDTO{EstimateOfframpDTO: offrampInput(), EstimateID: estimateID})
	require.ErrorIs(t, err, ramp.ErrEstimateConsumed)
}

// A quote for the other direction reads as missing rather than as a mismatch:
// distinguishing them would let a caller probe for ids that are not theirs.
func TestCreateOfframpRejectsEstimateFromTheOtherRail(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	h.mfa.EXPECT().Enrolled(testifymock.Anything).Return(false, nil).Once()
	h.estimateRepo.EXPECT().FindByCustomerAndExternalID(userID, estimateID).
		Return(storedEstimate(models.RailPixPayin), nil).Once()

	_, _, err := h.offramp.Create(context.Background(), user, person,
		dto.CreateOfframpDTO{EstimateOfframpDTO: offrampInput(), EstimateID: estimateID})
	require.ErrorIs(t, err, ramps.ErrEstimateNotFound)
}

// --- create: dedup and failover --------------------------------------------

// The retry of a create whose response was lost must return the first order,
// not place a second.
func TestCreateOfframpReturnsExistingOrderWithoutCallingUpstream(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	h.mfa.EXPECT().Enrolled(testifymock.Anything).Return(false, nil).Once()

	stored := storedOfframpEstimate()
	h.estimateRepo.EXPECT().FindByCustomerAndExternalID(userID, estimateID).Return(stored, nil).Once()
	h.txRepo.EXPECT().FindByCustomerAndEstimate(userID, models.ProviderBridge, stored.ID, models.TransactionTypeOfframp).
		Return(&models.Transaction{
			ExternalID: "existing", Provider: models.ProviderBridge,
			Type:   models.TransactionTypeOfframp,
			Status: models.TxStatusAwaitingFunds, AmountFiat: "98.50", AmountToken: "18.500000",
		}, nil).Once()

	view, created, err := h.offramp.Create(context.Background(), user, person,
		dto.CreateOfframpDTO{EstimateOfframpDTO: offrampInput(), EstimateID: estimateID})
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, "existing", view.ID)
	h.adapter.AssertNotCalled(t, "CreateOfframp",
		testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// The core money-safety property. A create that timed out is unknown state:
// the provider may well have accepted the order, and placing it again
// elsewhere is how one payment becomes two.
func TestCreateOfframpDoesNotFailOverAfterTheRequestWentOut(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge, models.ProviderOzav)
	h.mfa.EXPECT().Enrolled(testifymock.Anything).Return(false, nil).Once()

	stored := storedOfframpEstimate()
	h.estimateRepo.EXPECT().FindByCustomerAndExternalID(userID, estimateID).Return(stored, nil).Once()
	h.txRepo.EXPECT().FindByCustomerAndEstimate(userID, models.ProviderBridge, stored.ID, models.TransactionTypeOfframp).
		Return(nil, nil).Twice()
	h.txRepo.EXPECT().Create(testifymock.Anything).Return(nil).Once()

	h.adapter.EXPECT().CreateOfframp(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(nil, errors.New("bridge: POST /transfers: context deadline exceeded")).Once()

	_, _, err := h.offramp.Create(context.Background(), user, person,
		dto.CreateOfframpDTO{EstimateOfframpDTO: offrampInput(), EstimateID: estimateID})
	require.Error(t, err)
	h.second.AssertNotCalled(t, "CreateOfframp",
		testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// The row exists before the call, so a create that times out leaves something
// for the retry and the reconciler to find.
func TestCreateOfframpPersistsTheRowBeforeCallingUpstream(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	h.mfa.EXPECT().Enrolled(testifymock.Anything).Return(false, nil).Once()

	stored := storedOfframpEstimate()
	h.estimateRepo.EXPECT().FindByCustomerAndExternalID(userID, estimateID).Return(stored, nil).Once()
	h.txRepo.EXPECT().FindByCustomerAndEstimate(userID, models.ProviderBridge, stored.ID, models.TransactionTypeOfframp).
		Return(nil, nil).Twice()

	var order []string
	var persisted *models.Transaction
	h.txRepo.EXPECT().Create(testifymock.Anything).Return(nil).Once().
		Run(func(args testifymock.Arguments) {
			order = append(order, "persist")
			persisted = args.Get(0).(*models.Transaction)
		})
	h.adapter.EXPECT().CreateOfframp(testifymock.Anything, testifymock.Anything,
		testifymock.MatchedBy(func(req ramp.OfframpCreateRequest) bool {
			// The key the row already carries is the one the provider sees.
			return persisted != nil && req.IdempotencyKey == *persisted.IdempotencyKey
		})).
		Return(&ramp.RampOrder{
			ProviderOrderID: "trf_2", Status: models.TxStatusAwaitingFunds,
			DepositInstructions: map[string]any{"to_address": "BridgeDepositAddr"},
			Amounts:             map[string]string{"destination_amount": "98.50"},
			FeeAmount:           "0.277500", FeeCurrency: "usdc",
		}, nil).Once().
		Run(func(testifymock.Arguments) { order = append(order, "upstream") })
	h.txRepo.EXPECT().Update(testifymock.Anything).Return(nil).Once()
	h.estimateRepo.EXPECT().MarkConsumed(stored).Return(nil).Once()
	h.broadcaster.EXPECT().Publish(userID, testifymock.Anything).Once()

	view, created, err := h.offramp.Create(context.Background(), user, person,
		dto.CreateOfframpDTO{EstimateOfframpDTO: offrampInput(), EstimateID: estimateID})
	require.NoError(t, err)
	require.True(t, created)

	require.Equal(t, []string{"persist", "upstream"}, order)
	require.NotNil(t, persisted.IdempotencyKey)
	require.Equal(t, uint(42), *persisted.EstimateID)
	require.Equal(t, models.TxStatusAwaitingFunds, view.Status)
	require.Equal(t, "BridgeDepositAddr", view.DepositInstructions["to_address"])
	require.Equal(t, "98.50", view.AmountBRL, "the settled amount comes from the provider's receipt")
}

// --- create: the reservation row --------------------------------------------

// The row is written before the call, so it needs a deadline of its own. Without
// one the reconciler's expire() skips it (no ExpiresAt) and refresh() skips it
// (no gateway order id) — it is swept forever and resolved never.
func TestCreateOfframpReservesTheRowWithADeadline(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	h.mfa.EXPECT().Enrolled(testifymock.Anything).Return(false, nil).Once()

	stored := storedOfframpEstimate()
	h.estimateRepo.EXPECT().FindByCustomerAndExternalID(userID, estimateID).Return(stored, nil).Once()
	h.txRepo.EXPECT().FindByCustomerAndEstimate(userID, models.ProviderBridge, stored.ID, models.TransactionTypeOfframp).
		Return(nil, nil).Twice()

	var reserved *models.Transaction
	h.txRepo.EXPECT().Create(testifymock.Anything).Return(nil).Once().
		Run(func(args testifymock.Arguments) { reserved = args.Get(0).(*models.Transaction) })
	h.adapter.EXPECT().CreateOfframp(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(nil, errors.New("bridge: POST /transfers: context deadline exceeded")).Once()

	_, _, err := h.offramp.Create(context.Background(), user, person,
		dto.CreateOfframpDTO{EstimateOfframpDTO: offrampInput(), EstimateID: estimateID})
	require.Error(t, err)

	require.NotNil(t, reserved.ExpiresAt, "a row with no deadline is invisible to the reconciler forever")
	require.True(t, reserved.ExpiresAt.After(time.Now()))
	require.NotNil(t, reserved.EstimateID, "unknown state keeps the quote — a retry must not place a second order")
}

// The provider's answer governs the deposit window, including when the answer
// is "no window". Leaving the reservation deadline in place would have the
// reconciler expire an order the provider considers open.
func TestCreateOfframpReplacesTheReservationDeadline(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	h.mfa.EXPECT().Enrolled(testifymock.Anything).Return(false, nil).Once()

	stored := storedOfframpEstimate()
	h.estimateRepo.EXPECT().FindByCustomerAndExternalID(userID, estimateID).Return(stored, nil).Once()
	h.txRepo.EXPECT().FindByCustomerAndEstimate(userID, models.ProviderBridge, stored.ID, models.TransactionTypeOfframp).
		Return(nil, nil).Twice()
	h.txRepo.EXPECT().Create(testifymock.Anything).Return(nil).Once()
	h.adapter.EXPECT().CreateOfframp(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(&ramp.RampOrder{
			ProviderOrderID: "trf_2", Status: models.TxStatusAwaitingFunds,
		}, nil).Once()

	var saved *models.Transaction
	h.txRepo.EXPECT().Update(testifymock.Anything).Return(nil).Once().
		Run(func(args testifymock.Arguments) { saved = args.Get(0).(*models.Transaction) })
	h.estimateRepo.EXPECT().MarkConsumed(stored).Return(nil).Once()
	h.broadcaster.EXPECT().Publish(userID, testifymock.Anything).Once()

	view, created, err := h.offramp.Create(context.Background(), user, person,
		dto.CreateOfframpDTO{EstimateOfframpDTO: offrampInput(), EstimateID: estimateID})
	require.NoError(t, err)
	require.True(t, created)
	require.Nil(t, saved.ExpiresAt, "the provider reported no window, so the row must not carry one")
	require.Nil(t, view.ExpiresAt)
}

// The adapter proved the bytes never left. There is no upstream order to
// deduplicate against, so holding the quote would lock the user out of it for
// nothing — and the unique on (user_id, provider, estimate_id) is what does the
// holding, which is why the link is dropped and not only the status.
func TestCreateOfframpReleasesTheQuoteWhenTheRequestNeverWentOut(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	h.mfa.EXPECT().Enrolled(testifymock.Anything).Return(false, nil).Once()

	stored := storedOfframpEstimate()
	h.estimateRepo.EXPECT().FindByCustomerAndExternalID(userID, estimateID).Return(stored, nil).Once()
	h.txRepo.EXPECT().FindByCustomerAndEstimate(userID, models.ProviderBridge, stored.ID, models.TransactionTypeOfframp).
		Return(nil, nil).Twice()
	h.txRepo.EXPECT().Create(testifymock.Anything).Return(nil).Once()
	h.adapter.EXPECT().CreateOfframp(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(nil, fmt.Errorf("bridge: POST /transfers: %w: connection refused", ramp.ErrRequestNotSent)).Once()

	var released *models.Transaction
	h.txRepo.EXPECT().Update(testifymock.Anything).Return(nil).Once().
		Run(func(args testifymock.Arguments) { released = args.Get(0).(*models.Transaction) })

	_, _, err := h.offramp.Create(context.Background(), user, person,
		dto.CreateOfframpDTO{EstimateOfframpDTO: offrampInput(), EstimateID: estimateID})
	require.Error(t, err)

	require.Equal(t, models.TxStatusFailed, released.Status)
	require.Nil(t, released.EstimateID, "the quote must be free for the user to redeem again")
	require.Nil(t, released.ExpiresAt)
	require.NotNil(t, released.FailureReason)
	// The estimate itself is untouched: nothing consumed it.
	h.estimateRepo.AssertNotCalled(t, "MarkConsumed", testifymock.Anything)
}

// --- off-ramp ---------------------------------------------------------------

// The off-ramp sends money to an arbitrary PIX key. Step-up runs before
// anything else, including loading the quote.
func TestCreateOfframpRequiresMfaStepUp(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	h.mfa.EXPECT().Enrolled(user).Return(true, nil).Once()

	_, _, err := h.offramp.Create(context.Background(), user, person,
		dto.CreateOfframpDTO{EstimateOfframpDTO: offrampInput(), EstimateID: estimateID})
	require.ErrorIs(t, err, ramp.ErrMfaStepUpRequired)
	h.estimateRepo.AssertNotCalled(t, "FindByCustomerAndExternalID", testifymock.Anything, testifymock.Anything)
}

// Without a quote the BRL leg is unknown, and the rail's limits are expressed
// in BRL — so the order cannot be checked and is refused.
func TestCreateOfframpRequiresAnEstimate(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	h.mfa.EXPECT().Enrolled(testifymock.Anything).Return(false, nil).Once()

	_, _, err := h.offramp.Create(context.Background(), user, person,
		dto.CreateOfframpDTO{EstimateOfframpDTO: offrampInput()})
	require.ErrorIs(t, err, ramp.ErrEstimateRequired)
}

func TestCreateOfframpRejectsEstimateMismatch(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	h.mfa.EXPECT().Enrolled(testifymock.Anything).Return(false, nil).Once()

	stored := storedOfframpEstimate()
	h.estimateRepo.EXPECT().FindByCustomerAndExternalID(userID, estimateID).Return(stored, nil).Once()

	in := offrampInput()
	in.AmountToken = "1.000000"
	_, _, err := h.offramp.Create(context.Background(), user, person,
		dto.CreateOfframpDTO{EstimateOfframpDTO: in, EstimateID: estimateID})
	require.ErrorIs(t, err, ramp.ErrEstimateMismatch)
	h.adapter.AssertNotCalled(t, "CreateOfframp",
		testifymock.Anything, testifymock.Anything, testifymock.Anything)
	h.txRepo.AssertNotCalled(t, "Create", testifymock.Anything)
}

// Create converts with the snapshot's own amounts, not the ones a fresh quote
// would produce. Without this the two call sites could drift apart unnoticed.
func TestCreateOfframpLimitRefusalCarriesTheBoundInTokens(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	h.withLimits(t, models.RailPixPayout, "20.00", "", models.ProviderBridge)
	h.mfa.EXPECT().Enrolled(testifymock.Anything).Return(false, nil).Once()
	h.estimateRepo.EXPECT().FindByCustomerAndExternalID(userID, estimateID).
		Return(storedOfframpEstimate(), nil).Once()

	_, _, err := h.offramp.Create(context.Background(), user, person,
		dto.CreateOfframpDTO{EstimateOfframpDTO: offrampInput(), EstimateID: estimateID})

	require.ErrorIs(t, err, ramp.ErrAmountBelowMinimum)
	var violation *ramp.LimitViolation
	require.ErrorAs(t, err, &violation)
	// The snapshot quotes 18.500000 at 18.222500. The R$20 floor sits at
	// 18.5 × 20 / 18.2175 = 20.3101… of them, rounded up.
	require.Equal(t, "20.32", violation.LimitToken)
	h.adapter.AssertNotCalled(t, "CreateOfframp",
		testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

func TestCreateOfframpPlacesTheOrder(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	h.mfa.EXPECT().Enrolled(testifymock.Anything).Return(false, nil).Once()

	stored := storedOfframpEstimate()
	h.estimateRepo.EXPECT().FindByCustomerAndExternalID(userID, estimateID).Return(stored, nil).Once()
	h.txRepo.EXPECT().FindByCustomerAndEstimate(userID, models.ProviderBridge, stored.ID, models.TransactionTypeOfframp).
		Return(nil, nil).Twice()

	var persisted *models.Transaction
	h.txRepo.EXPECT().Create(testifymock.Anything).Return(nil).Once().
		Run(func(args testifymock.Arguments) { persisted = args.Get(0).(*models.Transaction) })
	h.adapter.EXPECT().CreateOfframp(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(&ramp.RampOrder{
			ProviderOrderID: "trf_2", Status: models.TxStatusAwaitingFunds,
			DepositInstructions: map[string]any{"to_address": "BridgeDepositAddr"},
			FeeAmount:           "0.277500", FeeCurrency: "usdc",
		}, nil).Once()
	h.txRepo.EXPECT().Update(testifymock.Anything).Return(nil).Once()
	h.estimateRepo.EXPECT().MarkConsumed(stored).Return(nil).Once()
	h.broadcaster.EXPECT().Publish(userID, testifymock.Anything).Once()

	view, created, err := h.offramp.Create(context.Background(), user, person,
		dto.CreateOfframpDTO{EstimateOfframpDTO: offrampInput(), EstimateID: estimateID})
	require.NoError(t, err)
	require.True(t, created)

	require.Equal(t, wallet, *persisted.FromAddress, "the crypto leaves the user's own wallet")
	require.Equal(t, pixKey, *persisted.PixKey)
	require.Equal(t, "BridgeDepositAddr", view.DepositInstructions["to_address"])
	require.Equal(t, "usdc", view.FeeCurrency)
}

func TestCreateOfframpExposesSimulatedDepositModeInSandbox(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	h.mfa.EXPECT().Enrolled(testifymock.Anything).Return(false, nil).Once()

	stored := storedOfframpEstimate()
	h.estimateRepo.EXPECT().FindByCustomerAndExternalID(userID, estimateID).Return(stored, nil).Once()
	h.txRepo.EXPECT().FindByCustomerAndEstimate(userID, models.ProviderBridge, stored.ID, models.TransactionTypeOfframp).
		Return(nil, nil).Twice()

	var persisted *models.Transaction
	h.txRepo.EXPECT().Create(testifymock.Anything).Return(nil).Once().
		Run(func(args testifymock.Arguments) { persisted = args.Get(0).(*models.Transaction) })
	h.adapter.EXPECT().CreateOfframp(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(&ramp.RampOrder{
			ProviderOrderID: "trf_2", Status: models.TxStatusAwaitingFunds,
			DepositInstructions: map[string]any{"to_address": "BridgeDepositAddr"},
			FeeAmount:           "0.277500", FeeCurrency: "usdc",
		}, nil).Once()
	h.txRepo.EXPECT().Update(testifymock.Anything).Return(nil).Once()
	h.estimateRepo.EXPECT().MarkConsumed(stored).Return(nil).Once()
	h.broadcaster.EXPECT().Publish(userID, testifymock.Anything).Once()

	view, created, err := h.offramp.Create(context.Background(), user, person,
		dto.CreateOfframpDTO{EstimateOfframpDTO: offrampInput(), EstimateID: estimateID})
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, ramps.DepositModeSimulated, view.DepositMode)
	require.NotNil(t, persisted.DepositMode)
	require.Equal(t, ramps.DepositModeSimulated, *persisted.DepositMode)
}

// --- create: the payout destination -----------------------------------------

// placedOfframp is what a create reserved and sent to the provider.
type placedOfframp struct {
	tx  *models.Transaction
	req ramp.OfframpCreateRequest
}

// expectPlacedOfframp stubs a create that reaches the provider and has order
// accepted.
func (h *harness) expectPlacedOfframp(stored *models.RampEstimate, order *ramp.RampOrder) *placedOfframp {
	placed := &placedOfframp{}
	h.mfa.EXPECT().Enrolled(testifymock.Anything).Return(false, nil).Once()
	h.estimateRepo.EXPECT().FindByCustomerAndExternalID(userID, estimateID).Return(stored, nil).Once()
	h.txRepo.EXPECT().FindByCustomerAndEstimate(userID, models.ProviderBridge, stored.ID, models.TransactionTypeOfframp).
		Return(nil, nil).Twice()
	h.txRepo.EXPECT().Create(testifymock.Anything).Return(nil).Once().
		Run(func(args testifymock.Arguments) { placed.tx = args.Get(0).(*models.Transaction) })
	h.adapter.EXPECT().CreateOfframp(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		RunAndReturn(func(_ context.Context, _ ramp.CustomerRef, req ramp.OfframpCreateRequest) (*ramp.RampOrder, error) {
			placed.req = req
			return order, nil
		}).Once()
	h.txRepo.EXPECT().Update(testifymock.Anything).Return(nil).Once()
	h.estimateRepo.EXPECT().MarkConsumed(stored).Return(nil).Once()
	h.broadcaster.EXPECT().Publish(userID, testifymock.Anything).Once()
	return placed
}

// A provider registers a PIX key as an object of its own the first time it pays
// it, and the id comes back on the order. The next order to that key has to name
// it: Bridge answers a second registration of a key it already holds with a 422,
// hidden only while the first call's idempotency key is still cached. Every
// repeat withdrawal a day later failed in production on 16/09 as
// provider_unavailable.
func TestCreateOfframpNamesTheDestinationAnEarlierOrderRegistered(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	h.knownDestination = "ext_registered"
	placed := h.expectPlacedOfframp(storedOfframpEstimate(), &ramp.RampOrder{
		ProviderOrderID: "trf_2", Status: models.TxStatusAwaitingFunds,
		ProviderDestinationRef: "ext_registered",
	})

	_, created, err := h.offramp.Create(context.Background(), user, person,
		dto.CreateOfframpDTO{EstimateOfframpDTO: offrampInput(), EstimateID: estimateID})
	require.NoError(t, err)
	require.True(t, created)

	// A CPF key is its own document, so the lookup does not narrow on one.
	require.Equal(t, []models.OfframpDestination{{
		CustomerID: userID, Provider: models.ProviderBridge, PixKeyHash: pixkeys.HashKey("cpf", pixKey),
	}}, h.destinationLookups)
	require.Equal(t, "ext_registered", placed.req.ExternalAccountID)
	require.Equal(t, "ext_registered", *placed.tx.ProviderDestinationRef)
	require.Empty(t, h.upsertedDestinations, "a destination already recorded is not recorded again")
}

// Issue #73: the same key typed another way is the same destination. Looking
// it up by what the user typed missed the ref of an earlier order to
// "+55 11 99999-8888" when this one says "(11) 99999-8888", and Bridge refused
// to register the key a second time.
func TestCreateOfframpLooksTheDestinationUpByTheNormalizedKey(t *testing.T) {
	const typed, document = "(11) 99999-8888", "39053344705"
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	h.knownDestination = "ext_registered"
	stored := storedOfframpEstimate()
	stored.RawResponse = strings.Replace(stored.RawResponse,
		`"pix_key":"`+pixKey+`","pix_key_type":"cpf"`,
		`"pix_key":"`+typed+`","pix_key_type":"phone","recipient_document":"`+document+`"`, 1)
	placed := h.expectPlacedOfframp(stored, &ramp.RampOrder{
		ProviderOrderID: "trf_2", Status: models.TxStatusAwaitingFunds,
		ProviderDestinationRef: "ext_registered",
	})
	in := offrampInput()
	in.PixKey, in.PixKeyType, in.RecipientDocument = typed, "phone", document

	_, _, err := h.offramp.Create(context.Background(), user, person,
		dto.CreateOfframpDTO{EstimateOfframpDTO: in, EstimateID: estimateID})
	require.NoError(t, err)

	require.Equal(t, []models.OfframpDestination{{
		CustomerID: userID, Provider: models.ProviderBridge,
		PixKeyHash:   pixkeys.HashKey("phone", "+5511999998888"),
		DocumentHash: pixkeys.DestinationDocumentHash("phone", document),
	}}, h.destinationLookups)
	require.Equal(t, "ext_registered", placed.req.ExternalAccountID)
}

// The first order to a key is what the next one finds: the destination the
// provider registered for it is recorded under the key's identity.
func TestCreateOfframpRecordsTheDestinationTheProviderRegistered(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	placed := h.expectPlacedOfframp(storedOfframpEstimate(), &ramp.RampOrder{
		ProviderOrderID: "trf_2", Status: models.TxStatusAwaitingFunds,
		ProviderDestinationRef: "ext_new",
	})

	_, created, err := h.offramp.Create(context.Background(), user, person,
		dto.CreateOfframpDTO{EstimateOfframpDTO: offrampInput(), EstimateID: estimateID})
	require.NoError(t, err)
	require.True(t, created)

	require.Empty(t, placed.req.ExternalAccountID, "a first order leaves the registration to the provider")
	require.Equal(t, []models.OfframpDestination{{
		CustomerID: userID, Provider: models.ProviderBridge, PixKeyHash: pixkeys.HashKey("cpf", pixKey),
		DestinationRef: "ext_new",
	}}, h.upsertedDestinations)
	require.Equal(t, "ext_new", *placed.tx.ProviderDestinationRef)
}

// A provider that registers nothing leaves nothing to record.
func TestCreateOfframpRecordsNoDestinationWhenTheProviderReportsNone(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	placed := h.expectPlacedOfframp(storedOfframpEstimate(),
		&ramp.RampOrder{ProviderOrderID: "trf_2", Status: models.TxStatusAwaitingFunds})

	_, created, err := h.offramp.Create(context.Background(), user, person,
		dto.CreateOfframpDTO{EstimateOfframpDTO: offrampInput(), EstimateID: estimateID})
	require.NoError(t, err)
	require.True(t, created)

	require.Empty(t, h.upsertedDestinations)
	require.Nil(t, placed.tx.ProviderDestinationRef)
}

// The transfer already exists upstream when the destination is recorded. An
// error there would have the client retry an order that was placed, so it is
// logged and the order is answered.
func TestCreateOfframpAnswersTheOrderWhenRecordingTheDestinationFails(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	h.destinationUpsertErr = errors.New("db down")
	placed := h.expectPlacedOfframp(storedOfframpEstimate(), &ramp.RampOrder{
		ProviderOrderID: "trf_2", Status: models.TxStatusAwaitingFunds,
		ProviderDestinationRef: "ext_new",
	})

	view, created, err := h.offramp.Create(context.Background(), user, person,
		dto.CreateOfframpDTO{EstimateOfframpDTO: offrampInput(), EstimateID: estimateID})
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, placed.tx.ExternalID, view.ID)
}

// The lookup runs before the reservation, so a failed read leaves nothing
// behind: no row for the reconciler to sweep, and a quote the user can retry.
// Guessing "no destination" instead would re-register the key, which is the
// 422 this lookup exists to avoid.
func TestCreateOfframpReservesNothingWhenTheDestinationLookupFails(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	h.destinationLookupErr = errors.New("db down")
	h.mfa.EXPECT().Enrolled(testifymock.Anything).Return(false, nil).Once()

	stored := storedOfframpEstimate()
	h.estimateRepo.EXPECT().FindByCustomerAndExternalID(userID, estimateID).Return(stored, nil).Once()
	h.txRepo.EXPECT().FindByCustomerAndEstimate(userID, models.ProviderBridge, stored.ID, models.TransactionTypeOfframp).
		Return(nil, nil).Twice()

	_, _, err := h.offramp.Create(context.Background(), user, person,
		dto.CreateOfframpDTO{EstimateOfframpDTO: offrampInput(), EstimateID: estimateID})
	require.ErrorContains(t, err, "db down")

	h.txRepo.AssertNotCalled(t, "Create", testifymock.Anything)
	h.adapter.AssertNotCalled(t, "CreateOfframp",
		testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// --- per-provider fees ------------------------------------------------------

// The fee is resolved per attempt: when the head of the order fails, the
// provider that answers prices with its own fee, and that is the fee the user
// is shown and the quote keeps.
func TestEstimateOnrampFailsOverWithTheServingProvidersFee(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderBridge, models.ProviderOzav)
	h.withFees(map[string]string{models.ProviderBridge: "1.50", models.ProviderOzav: "0.80"})

	h.adapter.EXPECT().EstimateOnramp(testifymock.Anything, testifymock.Anything,
		testifymock.MatchedBy(func(req ramp.OnrampQuoteRequest) bool { return req.FeePercent == "1.50" })).
		Return(nil, ramp.ErrProviderFailed).Once()
	h.second.EXPECT().EstimateOnramp(testifymock.Anything, testifymock.Anything,
		testifymock.MatchedBy(func(req ramp.OnrampQuoteRequest) bool { return req.FeePercent == "0.80" })).
		Return(bridgeEstimate(), nil).Once()
	var stored *models.RampEstimate
	h.estimateRepo.EXPECT().Create(testifymock.Anything).Return(nil).Once().
		Run(func(args testifymock.Arguments) { stored = args.Get(0).(*models.RampEstimate) })

	view, err := h.onramp.Estimate(context.Background(), user, person, onrampInput())

	require.NoError(t, err)
	require.Equal(t, models.ProviderOzav, stored.Provider)
	require.Equal(t, "0.80", stored.FeePercent, "the quote keeps the fee of the provider that priced it")
	require.Equal(t, "0.80", view.FeePercent)
}

func TestEstimateOfframpChargesTheServingProvidersFee(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge, models.ProviderOzav)
	h.withFees(map[string]string{models.ProviderBridge: "2.00", models.ProviderOzav: "0.50"})
	h.adapterServes = servesToken("USDC")
	h.secondServes = servesToken("USDT")

	h.second.EXPECT().EstimateOfframp(testifymock.Anything, testifymock.Anything,
		testifymock.MatchedBy(func(req ramp.OfframpQuoteRequest) bool { return req.FeePercent == "0.50" })).
		Return(bridgeEstimate(), nil).Once()
	var stored *models.RampEstimate
	h.estimateRepo.EXPECT().Create(testifymock.Anything).Return(nil).Once().
		Run(func(args testifymock.Arguments) { stored = args.Get(0).(*models.RampEstimate) })

	in := offrampInput()
	in.Token = "usdt"
	view, err := h.offramp.Estimate(context.Background(), user, person, in)

	require.NoError(t, err)
	require.Equal(t, "0.50", stored.FeePercent)
	require.Equal(t, "0.50", view.FeePercent)
}

// The create re-reads the fee of the quote's provider at the moment it places
// the order (spec §2): a fee changed after the quote reaches the provider.
func TestCreateOnrampSendsTheQuotesProvidersCurrentFee(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderOzav)
	h.withFees(map[string]string{models.ProviderBridge: "3.00", models.ProviderOzav: "0.90"})
	stored := storedOnrampEstimate()
	h.estimateRepo.EXPECT().FindByCustomerAndExternalID(userID, estimateID).Return(stored, nil).Once()
	h.txRepo.EXPECT().FindByCustomerAndEstimate(userID, models.ProviderOzav, stored.ID, models.TransactionTypeOnramp).
		Return(nil, nil).Twice()
	var persisted *models.Transaction
	h.txRepo.EXPECT().Create(testifymock.Anything).Return(nil).Once().
		Run(func(args testifymock.Arguments) { persisted = args.Get(0).(*models.Transaction) })
	h.second.EXPECT().CreateOnramp(testifymock.Anything, testifymock.Anything,
		testifymock.MatchedBy(func(req ramp.OnrampCreateRequest) bool { return req.FeePercent == "0.90" })).
		Return(&ramp.RampOrder{ProviderOrderID: "ozav_tr_1", Status: models.TxStatusAwaitingFunds}, nil).Once()
	h.txRepo.EXPECT().Update(testifymock.Anything).Return(nil).Once()
	h.estimateRepo.EXPECT().MarkConsumed(stored).Return(nil).Once()
	h.broadcaster.EXPECT().Publish(userID, testifymock.Anything).Once()

	_, _, err := h.onramp.Create(context.Background(), user, person,
		dto.CreateOnrampDTO{EstimateOnrampDTO: onrampInput(), EstimateID: estimateID})

	require.NoError(t, err)
	require.NotNil(t, persisted.FeePercent)
	require.Equal(t, "0.90", *persisted.FeePercent, "Ozav's fee, not Bridge's and not the quoted 1.50")
}

func TestCreateOfframpSendsTheQuotesProvidersCurrentFee(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	h.withFees(map[string]string{models.ProviderBridge: "2.25", models.ProviderOzav: "0.40"})
	h.mfa.EXPECT().Enrolled(testifymock.Anything).Return(false, nil).Once()
	stored := storedOfframpEstimate()
	h.estimateRepo.EXPECT().FindByCustomerAndExternalID(userID, estimateID).Return(stored, nil).Once()
	h.txRepo.EXPECT().FindByCustomerAndEstimate(userID, models.ProviderBridge, stored.ID, models.TransactionTypeOfframp).
		Return(nil, nil).Twice()
	var persisted *models.Transaction
	h.txRepo.EXPECT().Create(testifymock.Anything).Return(nil).Once().
		Run(func(args testifymock.Arguments) { persisted = args.Get(0).(*models.Transaction) })
	h.adapter.EXPECT().CreateOfframp(testifymock.Anything, testifymock.Anything,
		testifymock.MatchedBy(func(req ramp.OfframpCreateRequest) bool { return req.FeePercent == "2.25" })).
		Return(&ramp.RampOrder{
			ProviderOrderID: "trf_2", Status: models.TxStatusAwaitingFunds,
			DepositInstructions: map[string]any{"to_address": "BridgeDepositAddr"},
		}, nil).Once()
	h.txRepo.EXPECT().Update(testifymock.Anything).Return(nil).Once()
	h.estimateRepo.EXPECT().MarkConsumed(stored).Return(nil).Once()
	h.broadcaster.EXPECT().Publish(userID, testifymock.Anything).Once()

	_, _, err := h.offramp.Create(context.Background(), user, person,
		dto.CreateOfframpDTO{EstimateOfframpDTO: offrampInput(), EstimateID: estimateID})

	require.NoError(t, err)
	require.Equal(t, "2.25", *persisted.FeePercent)
}

// --- on-ramp create ---------------------------------------------------------

func storedOnrampEstimate() *models.RampEstimate {
	quoteID := "ozav_q_1"
	return &models.RampEstimate{
		Model: orm.Model{ID: 43}, ExternalID: estimateID, CustomerID: userID, UserID: userID,
		Provider: models.ProviderOzav, Rail: models.RailPixPayin, FeePercent: "1.50",
		ProviderQuoteID: &quoteID,
		RawRequest:      "{}",
		RawResponse: `{"rail":"` + models.RailPixPayin + `","source_amount":"100.00","source_currency":"brl",` +
			`"destination_currency":"usdc","destination_payment_rail":"solana",` +
			`"destination_address":"` + wallet + `",` +
			`"estimated_receive":"18.222500","rate":"0.185000","fee_percent":"1.50",` +
			`"fee_amount":"1.50","fee_currency":"brl"}`,
	}
}

// The on-ramp create demands an estimate for a reason the off-ramp's does not
// share: this rail's provider quote is binding and is what fixes the rate the
// user was shown, so a create without one would settle at whatever the rate is
// when it lands.
func TestCreateOnrampRequiresAnEstimate(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderOzav)

	_, _, err := h.onramp.Create(context.Background(), user, person,
		dto.CreateOnrampDTO{EstimateOnrampDTO: onrampInput()})
	require.ErrorIs(t, err, ramp.ErrEstimateRequired)
}

func TestCreateOnrampRejectsEstimateMismatch(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderOzav)
	stored := storedOnrampEstimate()
	h.estimateRepo.EXPECT().FindByCustomerAndExternalID(userID, estimateID).Return(stored, nil).Once()

	in := onrampInput()
	in.AmountBRL = "999.00"
	_, _, err := h.onramp.Create(context.Background(), user, person,
		dto.CreateOnrampDTO{EstimateOnrampDTO: in, EstimateID: estimateID})
	require.ErrorIs(t, err, ramp.ErrEstimateMismatch)
	h.second.AssertNotCalled(t, "CreateOnramp",
		testifymock.Anything, testifymock.Anything, testifymock.Anything)
	h.txRepo.AssertNotCalled(t, "Create", testifymock.Anything)
}

// The BR Code is what the user pays; an order that reaches the client without
// it is an order nobody can fund.
func TestCreateOnrampPlacesTheOrderAndReturnsTheBrCode(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderOzav)
	stored := storedOnrampEstimate()
	h.estimateRepo.EXPECT().FindByCustomerAndExternalID(userID, estimateID).Return(stored, nil).Once()
	h.txRepo.EXPECT().FindByCustomerAndEstimate(userID, models.ProviderOzav, stored.ID, models.TransactionTypeOnramp).
		Return(nil, nil).Twice()

	var persisted *models.Transaction
	h.txRepo.EXPECT().Create(testifymock.Anything).Return(nil).Once().
		Run(func(args testifymock.Arguments) { persisted = args.Get(0).(*models.Transaction) })

	expiry := time.Now().Add(15 * time.Minute)
	h.second.EXPECT().CreateOnramp(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(&ramp.RampOrder{
			ProviderOrderID: "ozav_tr_1", Status: models.TxStatusAwaitingFunds,
			DepositInstructions: map[string]any{
				"br_code": "00020126570014br.gov.bcb.pix", "intent_id": "sim_pix_1",
			},
			Amounts:   map[string]string{"source_amount": "100.00", "destination_amount": "18.222500"},
			ExpiresAt: &expiry,
		}, nil).Once()
	h.txRepo.EXPECT().Update(testifymock.Anything).Return(nil).Once()
	h.estimateRepo.EXPECT().MarkConsumed(stored).Return(nil).Once()
	h.broadcaster.EXPECT().Publish(userID, testifymock.Anything).Once()

	view, created, err := h.onramp.Create(context.Background(), user, person,
		dto.CreateOnrampDTO{EstimateOnrampDTO: onrampInput(), EstimateID: estimateID})
	require.NoError(t, err)
	require.True(t, created)

	require.Equal(t, "100.00", persisted.AmountFiat, "the BRL the user pays")
	require.Equal(t, wallet, persisted.DestinationWallet, "the crypto lands in the user's own wallet")
	require.Equal(t, "USDC", persisted.TargetToken)
	require.NotNil(t, persisted.Chain, "the delivery needs the network the crypto settles on")
	require.Equal(t, "solana", *persisted.Chain)
	require.Equal(t, "00020126570014br.gov.bcb.pix", view.DepositInstructions["br_code"])
	require.NotNil(t, view.ExpiresAt, "the BR Code expires and the client has to show that")
}

// The limits are expressed in BRL and the on-ramp's BRL leg is the request
// itself, so they are checked before any provider is called — unlike the
// off-ramp, where the BRL is only knowable from the quote.
func TestCreateOnrampRejectsAmountBelowMinimumWithoutCallingUpstream(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderOzav)
	h.withLimits(t, models.RailPixPayin, "400.00", "", models.ProviderOzav)

	_, _, err := h.onramp.Create(context.Background(), user, person,
		dto.CreateOnrampDTO{EstimateOnrampDTO: onrampInput(), EstimateID: estimateID})
	require.ErrorIs(t, err, ramp.ErrAmountBelowMinimum)
	h.second.AssertNotCalled(t, "CreateOnramp",
		testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// --- on-ramp delivery -------------------------------------------------------

func settledOnramp() *models.Transaction {
	chain, delivered := "polygon", (*string)(nil)
	tx := &models.Transaction{
		Model: orm.Model{ID: 77}, CustomerID: userID, UserID: userID, Provider: models.ProviderOzav,
		Type: models.TransactionTypeOnramp, ExternalID: "aaaaaaaa-1111-2222-3333-444444444444",
		Rail: support.Ptr(models.RailPixPayin), Status: models.TxStatusCompleted,
		GatewayOrderID: "ozav_tr_payin_1",
		AmountFiat:     "500.00", AmountToken: "88.200000", TargetToken: "USDC",
		DestinationWallet: wallet, Chain: &chain, DeliveryOrderID: delivered,
	}
	tx.CreatedAt = carbon.NewDateTime(carbon.FromStdTime(attestedAt))
	return tx
}

// attestedAt is when the user named their destination address on the order.
var attestedAt = time.Date(2026, 8, 31, 15, 18, 23, 0, time.UTC)

// The pay-in bought the crypto into the provider's custody. Delivering it is
// what the user is actually waiting for, and it sends the amount the pay-in
// really produced — never the quote's estimate.
func TestDeliverOnrampSendsWhatThePayInBought(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderOzav)
	tx := settledOnramp()
	h.delivering.order = &ramp.RampOrder{
		ProviderOrderID: "ozav_tr_delivery_1", Status: models.TxStatusCompleted,
	}
	h.txRepo.EXPECT().Update(testifymock.Anything).Return(nil).Once()
	// The delivery is what the user has been waiting for, so it reaches their
	// live stream like any other movement on the order.
	h.broadcaster.EXPECT().Publish(userID, testifymock.Anything).Once()

	require.NoError(t, h.onramp.Deliver(context.Background(), tx))

	require.NotNil(t, h.delivering.delivered, "the delivery must reach the provider")
	require.Equal(t, "88.200000", h.delivering.delivered.Amount,
		"the crypto the pay-in delivered, not the quote's estimate")
	require.Equal(t, wallet, h.delivering.delivered.Destination.Address,
		"the user's own wallet is the whole point of the second hop")
	require.Equal(t, "polygon", h.delivering.delivered.Destination.PaymentRail)
	require.Equal(t, "USDC", h.delivering.delivered.Destination.Currency)
	require.Equal(t, attestedAt.UTC(), h.delivering.delivered.AttestedOwnershipAt.UTC(),
		"ownership was attested when the user named the address, not when we send")
	require.NotEmpty(t, h.delivering.delivered.IdempotencyKey)

	require.NotNil(t, tx.DeliveryOrderID)
	require.Equal(t, "ozav_tr_delivery_1", *tx.DeliveryOrderID,
		"persisting it is what stops the next sweep paying the user twice")
}

// The guard that matters most: this is an irreversible send, and a duplicate
// webhook, a retry and the reconciler all reach the same row.
func TestDeliverOnrampDoesNothingWhenAlreadyDelivered(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderOzav)
	tx := settledOnramp()
	tx.DeliveryOrderID = support.Ptr("ozav_tr_delivery_1")

	require.NoError(t, h.onramp.Deliver(context.Background(), tx))
	require.Nil(t, h.delivering.delivered, "a second send would pay the user twice")
}

// Nothing has been bought yet, so there is nothing in custody to forward.
func TestDeliverOnrampDoesNothingBeforeThePayInSettles(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderOzav)
	tx := settledOnramp()
	tx.Status = models.TxStatusAwaitingFunds

	require.NoError(t, h.onramp.Deliver(context.Background(), tx))
	require.Nil(t, h.delivering.delivered)
}

// An off-ramp has no second hop: its crypto leaves the user's wallet, it does
// not arrive in one.
func TestDeliverOnrampIgnoresAnOfframp(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderOzav)
	tx := settledOnramp()
	tx.Type = models.TransactionTypeOfframp

	require.NoError(t, h.onramp.Deliver(context.Background(), tx))
	require.Nil(t, h.delivering.delivered)
}

// A provider that delivers straight to the address its account was provisioned
// against has nothing left to do. It is not an error, and it must not be
// reported as one — the order is already finished.
func TestDeliverOnrampIsANoOpForAProviderThatDeliversInOneHop(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderBridge)
	tx := settledOnramp()
	tx.Provider = models.ProviderBridge

	require.NoError(t, h.onramp.Deliver(context.Background(), tx),
		"a provider without the delivery port has already finished the order")
}

// A create the provider refused without sending anything must not leave a row
// behind that the retry hands back as an order.
//
// It is reachable on the first call, not an edge case: Bridge is the seeded
// head of pix_payin and collects through a standing account, so its
// CreateOnramp always refuses. Without the reservation being released,
// dedupByEstimate — which filters on (user, provider, estimate, type) and not
// on status — returns that pending row, and the client gets 200 with an order
// carrying no br_code and no expiry, which it can never fund.
// A refusal that proves the bytes never left frees the quote and stands as a
// failure the user can see: the provider was asked and could not be reached.
func TestCreateOnrampFailsTheReservationWhenTheRequestNeverWentOut(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderOzav)
	stored := storedOnrampEstimate()
	h.estimateRepo.EXPECT().FindByCustomerAndExternalID(userID, estimateID).Return(stored, nil).Once()
	h.txRepo.EXPECT().FindByCustomerAndEstimate(userID, models.ProviderOzav, stored.ID, models.TransactionTypeOnramp).
		Return(nil, nil).Twice()

	var reserved *models.Transaction
	h.txRepo.EXPECT().Create(testifymock.Anything).Return(nil).Once().
		Run(func(args testifymock.Arguments) { reserved = args.Get(0).(*models.Transaction) })

	h.second.EXPECT().CreateOnramp(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(nil, fmt.Errorf("ozav: POST /transfers: %w: connection refused", ramp.ErrRequestNotSent)).Once()
	h.txRepo.EXPECT().Update(testifymock.Anything).Return(nil).Once()

	in := dto.CreateOnrampDTO{EstimateOnrampDTO: onrampInput(), EstimateID: estimateID}
	_, _, err := h.onramp.Create(context.Background(), user, person, in)
	require.Error(t, err)

	require.Equal(t, models.TxStatusFailed, reserved.Status)
	require.NotNil(t, reserved.FailureReason)
	require.Nil(t, reserved.EstimateID, "the quote must be freed, not held by a dead row")
}

// A provider that collects through a standing account refuses this create
// before building a request — the app then asks for the account instead, and
// that deposit is the one that settles. Nothing failed, so nothing may be
// recorded as a failure: half of production's on-ramp rows were this artifact,
// each one reading as "provider unreachable" against a provider that answered
// instantly and correctly.
func TestCreateOnrampDiscardsTheReservationWhenTheProviderCollectsThroughAStandingAccount(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderOzav)
	stored := storedOnrampEstimate()
	h.estimateRepo.EXPECT().FindByCustomerAndExternalID(userID, estimateID).Return(stored, nil).Twice()
	h.txRepo.EXPECT().FindByCustomerAndEstimate(userID, models.ProviderOzav, stored.ID, models.TransactionTypeOnramp).
		Return(nil, nil).Times(4)

	var reserved *models.Transaction
	h.txRepo.EXPECT().Create(testifymock.Anything).Return(nil).Twice().
		Run(func(args testifymock.Arguments) { reserved = args.Get(0).(*models.Transaction) })

	refusal := fmt.Errorf("collected through a standing account: %w, %w",
		ramp.ErrNoProviderAvailable, ramp.ErrRequestNotSent)
	h.second.EXPECT().CreateOnramp(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(nil, refusal).Twice()

	var discarded *models.Transaction
	h.txRepo.EXPECT().DiscardReservation(testifymock.Anything).Return(nil).Twice().
		Run(func(args testifymock.Arguments) { discarded = args.Get(0).(*models.Transaction) })

	in := dto.CreateOnrampDTO{EstimateOnrampDTO: onrampInput(), EstimateID: estimateID}
	_, _, err := h.onramp.Create(context.Background(), user, person, in)
	require.Error(t, err)

	require.Same(t, reserved, discarded, "the reservation itself is what goes")
	require.NotEqual(t, models.TxStatusFailed, reserved.Status, "a negotiation is not a failed order")
	require.Nil(t, reserved.FailureReason)
	h.txRepo.AssertNotCalled(t, "Update", testifymock.Anything)

	// The retry refuses again for the same reason — never a 200 with a phantom
	// order the user cannot pay.
	_, created, err := h.onramp.Create(context.Background(), user, person, in)
	require.Error(t, err)
	require.False(t, created)
}

// --- recipient name ---------------------------------------------------------

func storedOfframpEstimateFor(recipient string) *models.RampEstimate {
	stored := storedOfframpEstimate()
	stored.RawResponse = strings.Replace(stored.RawResponse,
		`"pix_key":"`+pixKey+`"`,
		`"recipient_name":"`+recipient+`","pix_key":"`+pixKey+`"`, 1)
	return stored
}

// The name is stored normalized so the create has a stable thing to match, and
// so the provider is never asked to register a destination whose owner carries
// a trailing space.
func TestEstimateOfframpNormalizesTheRecipientNameItStores(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)

	var sentToProvider string
	h.adapter.EXPECT().EstimateOfframp(testifymock.Anything, testifymock.Anything,
		testifymock.MatchedBy(func(req ramp.OfframpQuoteRequest) bool {
			sentToProvider = req.Destination.AccountOwnerName
			return true
		})).
		Return(&ramp.Estimate{
			Rate: "5.405405", FeePercent: "1.50", FeeAmount: "0.277500",
			FeeCurrency: "usdc", EstimatedReceive: "98.50",
		}, nil).Once()

	var stored *models.RampEstimate
	h.estimateRepo.EXPECT().Create(testifymock.Anything).Return(nil).Once().
		Run(func(args testifymock.Arguments) { stored = args.Get(0).(*models.RampEstimate) })

	in := offrampInput()
	in.RecipientName = "  Maria  Silva  "
	_, err := h.offramp.Estimate(context.Background(), user, person, in)
	require.NoError(t, err)

	require.Equal(t, "Maria Silva", sentToProvider)
	require.Contains(t, stored.RawResponse, `"recipient_name":"Maria Silva"`)
}

// The app sends the name the user typed. A space at the end of one of the two
// calls is not a different recipient, and refusing it as one is how a valid
// order became 409 estimate_mismatch in production.
func TestCreateOfframpAcceptsARecipientNameThatDiffersOnlyByWhitespace(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	h.mfa.EXPECT().Enrolled(testifymock.Anything).Return(false, nil).Once()

	stored := storedOfframpEstimateFor("Maria Silva")
	h.estimateRepo.EXPECT().FindByCustomerAndExternalID(userID, estimateID).Return(stored, nil).Once()
	h.txRepo.EXPECT().FindByCustomerAndEstimate(userID, models.ProviderBridge, stored.ID, models.TransactionTypeOfframp).
		Return(nil, nil).Twice()

	var persisted *models.Transaction
	h.txRepo.EXPECT().Create(testifymock.Anything).Return(nil).Once().
		Run(func(args testifymock.Arguments) { persisted = args.Get(0).(*models.Transaction) })

	var sentToProvider string
	h.adapter.EXPECT().CreateOfframp(testifymock.Anything, testifymock.Anything,
		testifymock.MatchedBy(func(req ramp.OfframpCreateRequest) bool {
			sentToProvider = req.Destination.AccountOwnerName
			return true
		})).
		Return(&ramp.RampOrder{ProviderOrderID: "trf_2", Status: models.TxStatusAwaitingFunds}, nil).Once()
	h.txRepo.EXPECT().Update(testifymock.Anything).Return(nil).Once()
	h.estimateRepo.EXPECT().MarkConsumed(stored).Return(nil).Once()
	h.broadcaster.EXPECT().Publish(userID, testifymock.Anything).Once()

	in := offrampInput()
	in.RecipientName = "Maria Silva "
	_, created, err := h.offramp.Create(context.Background(), user, person,
		dto.CreateOfframpDTO{EstimateOfframpDTO: in, EstimateID: estimateID})
	require.NoError(t, err)
	require.True(t, created)

	require.Equal(t, "Maria Silva", sentToProvider, "the provider gets the normalized name")
	require.Equal(t, "Maria Silva", *persisted.RecipientName)
}

// --- where new business is born ----------------------------------------------

// rebuild rewires the order services after a test swapped one collaborator.
func (h *harness) rebuild(swap func(*ramps.Deps)) {
	swap(&h.deps)
	h.onramp = ramps.NewOnrampService(h.deps)
	h.offramp = ramps.NewOfframpService(h.deps, h.destinations, h.mfa, h.appEnv, nil)
}

// birth is a call that opens new business on a rail: the place a closed or
// unconfigured rail has to refuse, and where no read of an existing order goes.
type birth struct {
	name   string
	rail   string
	create bool
	call   func(*harness) error
}

var births = []birth{
	{"onramp estimate", models.RailPixPayin, false, func(h *harness) error {
		_, err := h.onramp.Estimate(context.Background(), user, person, onrampInput())
		return err
	}},
	{"onramp create", models.RailPixPayin, true, func(h *harness) error {
		_, _, err := h.onramp.Create(context.Background(), user, person,
			dto.CreateOnrampDTO{EstimateOnrampDTO: onrampInput(), EstimateID: estimateID})
		return err
	}},
	{"offramp estimate", models.RailPixPayout, false, func(h *harness) error {
		_, err := h.offramp.Estimate(context.Background(), user, person, offrampInput())
		return err
	}},
	{"offramp create", models.RailPixPayout, true, func(h *harness) error {
		h.mfa.EXPECT().Enrolled(user).Return(false, nil).Once()
		_, _, err := h.offramp.Create(context.Background(), user, person,
			dto.CreateOfframpDTO{EstimateOfframpDTO: offrampInput(), EstimateID: estimateID})
		return err
	}},
}

// A kill switch that stops working during a flag-store outage is not one.
func TestABirthOnAClosedRailIsRefused(t *testing.T) {
	gates := map[string]error{
		"switched off":    nil,
		"flag store down": errors.New("featureflag: redis: connection refused"),
	}
	for _, b := range births {
		for name, gateErr := range gates {
			t.Run(b.name+"/"+name, func(t *testing.T) {
				h := newHarness(t, b.rail, models.ProviderBridge)
				features := mocks.NewMockFeature(t)
				features.EXPECT().IsActive(testifymock.Anything, ramp.Rail(b.rail).Flag(), models.UserFlagScope(userID)).
					Return(false, gateErr).Once()
				h.rebuild(func(d *ramps.Deps) { d.Features = features })

				require.ErrorIs(t, b.call(h), ramp.ErrRailDisabled)
				h.estimateRepo.AssertNotCalled(t, "Create", testifymock.Anything)
				h.txRepo.AssertNotCalled(t, "Create", testifymock.Anything)
			})
		}
	}
}

// Not defaulting to a provider nobody chose, with limits nobody set.
func TestABirthOnARailWithNoProviderIsRefused(t *testing.T) {
	for _, b := range births {
		t.Run(b.name, func(t *testing.T) {
			h := newHarness(t, b.rail)

			require.ErrorIs(t, b.call(h), ramp.ErrNoProviderAvailable)
			h.txRepo.AssertNotCalled(t, "Create", testifymock.Anything)
		})
	}
}

// Another user's estimate and one that never existed are the same 404, so the
// create is not an oracle for other users' quote ids.
func TestACreateNamingAnEstimateTheCallerDoesNotHoldIsNotFound(t *testing.T) {
	for _, b := range births {
		if !b.create {
			continue
		}
		t.Run(b.name, func(t *testing.T) {
			h := newHarness(t, b.rail, models.ProviderBridge)
			h.estimateRepo.EXPECT().FindByCustomerAndExternalID(userID, estimateID).Return(nil, nil).Once()

			require.ErrorIs(t, b.call(h), ramps.ErrEstimateNotFound)
			h.txRepo.AssertNotCalled(t, "Create", testifymock.Anything)
		})
	}
}

// Fails closed on an unreadable enrollment; a wrong code keeps the validator's
// error (mfa_invalid), so the app offers the field again.
func TestCreateOfframpStepUp(t *testing.T) {
	unreadable := errors.New("mfa: load enrollment: connection reset")
	cases := map[string]struct {
		enrolled  bool
		lookupErr error
		codeErr   error
		want      error
	}{
		"enrollment unreadable": {lookupErr: unreadable, want: unreadable},
		"wrong code":            {enrolled: true, codeErr: mfa.ErrInvalidCode, want: mfa.ErrInvalidCode},
		"right code":            {enrolled: true, want: ramps.ErrEstimateNotFound},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
			h.mfa.EXPECT().Enrolled(user).Return(c.enrolled, c.lookupErr).Once()
			if c.enrolled {
				h.mfa.EXPECT().ValidateStepUpCode(testifymock.Anything, user, "123456", "totp").
					Return(c.codeErr).Once()
			}
			if c.want == ramps.ErrEstimateNotFound {
				h.estimateRepo.EXPECT().FindByCustomerAndExternalID(userID, estimateID).Return(nil, nil).Once()
			}

			_, _, err := h.offramp.Create(context.Background(), user, person, dto.CreateOfframpDTO{
				EstimateOfframpDTO: offrampInput(), EstimateID: estimateID,
				Code: "123456", CodeType: "totp",
			})
			require.ErrorIs(t, err, c.want)
			if c.want != ramps.ErrEstimateNotFound {
				h.estimateRepo.AssertNotCalled(t, "FindByCustomerAndExternalID", testifymock.Anything, testifymock.Anything)
			}
		})
	}
}

// --- create: both directions --------------------------------------------------

// direction is one side's create, stubbed up to the provider call. upstream
// counts the orders the provider was asked to place and accepts each.
type direction struct {
	name     string
	rail     string
	provider string
	txType   string
	estimate func() *models.RampEstimate
	upstream func(h *harness, placed *atomic.Int32)
	refuse   func(h *harness, err error)
	create   func(h *harness) (bool, error)
}

var directions = []direction{
	{
		name: "onramp", rail: models.RailPixPayin, provider: models.ProviderOzav,
		txType: models.TransactionTypeOnramp, estimate: storedOnrampEstimate,
		upstream: func(h *harness, placed *atomic.Int32) {
			h.second.EXPECT().CreateOnramp(testifymock.Anything, testifymock.Anything, testifymock.Anything).
				RunAndReturn(func(context.Context, ramp.CustomerRef, ramp.OnrampCreateRequest) (*ramp.RampOrder, error) {
					placed.Add(1)
					return awaitingFunds(), nil
				}).Maybe()
		},
		refuse: func(h *harness, err error) {
			h.second.EXPECT().CreateOnramp(testifymock.Anything, testifymock.Anything, testifymock.Anything).
				Return(nil, err).Once()
		},
		create: func(h *harness) (bool, error) {
			_, created, err := h.onramp.Create(context.Background(), user, person,
				dto.CreateOnrampDTO{EstimateOnrampDTO: onrampInput(), EstimateID: estimateID})
			return created, err
		},
	},
	{
		name: "offramp", rail: models.RailPixPayout, provider: models.ProviderBridge,
		txType: models.TransactionTypeOfframp, estimate: storedOfframpEstimate,
		upstream: func(h *harness, placed *atomic.Int32) {
			h.mfa.EXPECT().Enrolled(user).Return(false, nil).Maybe()
			h.adapter.EXPECT().CreateOfframp(testifymock.Anything, testifymock.Anything, testifymock.Anything).
				RunAndReturn(func(context.Context, ramp.CustomerRef, ramp.OfframpCreateRequest) (*ramp.RampOrder, error) {
					placed.Add(1)
					return awaitingFunds(), nil
				}).Maybe()
		},
		refuse: func(h *harness, err error) {
			h.mfa.EXPECT().Enrolled(user).Return(false, nil).Maybe()
			h.adapter.EXPECT().CreateOfframp(testifymock.Anything, testifymock.Anything, testifymock.Anything).
				Return(nil, err).Once()
		},
		create: func(h *harness) (bool, error) {
			_, created, err := h.offramp.Create(context.Background(), user, person,
				dto.CreateOfframpDTO{EstimateOfframpDTO: offrampInput(), EstimateID: estimateID})
			return created, err
		},
	},
}

func awaitingFunds() *ramp.RampOrder {
	return &ramp.RampOrder{ProviderOrderID: "order_1", Status: models.TxStatusAwaitingFunds}
}

// newDirectionHarness serves the direction's rail with its quote's provider
// first and the other one behind it, so a failover would have somewhere to go.
func newDirectionHarness(t *testing.T, d direction) *harness {
	t.Helper()
	h := newHarness(t, d.rail, d.provider, otherProvider(d.provider))
	h.estimateRepo.EXPECT().FindByCustomerAndExternalID(userID, estimateID).
		RunAndReturn(func(uint, string) (*models.RampEstimate, error) { return d.estimate(), nil }).Maybe()
	return h
}

func otherProvider(code string) string {
	if code == models.ProviderBridge {
		return models.ProviderOzav
	}
	return models.ProviderBridge
}

// Redeeming a quote with another provider would price the order differently
// from what the user agreed to.
func TestCreateRefusesWhenTheQuotesProviderNoLongerServes(t *testing.T) {
	for _, d := range directions {
		t.Run(d.name, func(t *testing.T) {
			h := newDirectionHarness(t, d)
			h.config(d.provider, false, models.ProviderModeSandbox)
			var placed atomic.Int32
			d.upstream(h, &placed)
			h.txRepo.EXPECT().FindByCustomerAndEstimate(userID, d.provider, d.estimate().ID, d.txType).
				Return(nil, nil).Twice()

			_, err := d.create(h)
			require.ErrorIs(t, err, ramp.ErrNoProviderAvailable)
			require.Zero(t, placed.Load())
			h.txRepo.AssertNotCalled(t, "Create", testifymock.Anything)
		})
	}
}

// A reservation that never recorded its provider order (its request died, or
// the write after the provider's answer failed) is the retry's order until its
// deadline; past it the retry resumes the row, and so its idempotency key, so
// the provider replays what it may hold instead of placing a second order.
func TestRetryOfAReservationWithoutItsOrder(t *testing.T) {
	notSent := fmt.Errorf("wallet lookup: %w", ramp.ErrRequestNotSent)
	cases := map[string]struct {
		deadline time.Duration
		refuse   error
		created  bool
	}{
		"in flight":                  {deadline: time.Minute},
		"stale":                      {deadline: -time.Minute, created: true},
		"stale, refused before send": {deadline: -time.Minute, refuse: notSent},
	}
	for _, d := range directions {
		for name, c := range cases {
			t.Run(d.name+"/"+name, func(t *testing.T) {
				h := newDirectionHarness(t, d)
				var placed atomic.Int32
				estimate, key, deadline := d.estimate().ID, "reserved-key", time.Now().Add(c.deadline)
				reserved := &models.Transaction{
					CustomerID: userID,
					UserID:     userID, Provider: d.provider, Type: d.txType, ExternalID: "reserved",
					Status: models.TxStatusPending, EstimateID: &estimate, IdempotencyKey: &key, ExpiresAt: &deadline,
				}
				h.txRepo.EXPECT().FindByCustomerAndEstimate(userID, d.provider, estimate, d.txType).Return(reserved, nil)
				switch {
				case c.refuse != nil:
					d.refuse(h, c.refuse)
					h.txRepo.EXPECT().Update(reserved).Return(nil).Once()
				case c.created:
					d.upstream(h, &placed)
					h.txRepo.EXPECT().Update(reserved).Return(nil).Twice()
					h.estimateRepo.EXPECT().MarkConsumed(testifymock.Anything).Return(nil).Once()
					h.broadcaster.EXPECT().Publish(userID, testifymock.Anything).Once()
				default:
					d.upstream(h, &placed)
				}

				created, err := d.create(h)
				require.Equal(t, c.refuse == nil, err == nil, err)
				require.Equal(t, c.created, created)
				h.txRepo.AssertNotCalled(t, "Create", testifymock.Anything)
				if c.created {
					require.EqualValues(t, 1, placed.Load())
					require.Equal(t, key, sentKey(h), "the provider must see the first attempt's key")
					require.Equal(t, "order_1", reserved.GatewayOrderID)
				}
				if c.refuse != nil {
					require.NotNil(t, reserved.EstimateID, "the first attempt is unknown state: the quote stays held")
				}
			})
		}
	}
}

// sentKey is the idempotency key of the create the provider received.
func sentKey(h *harness) string {
	for _, call := range append(h.adapter.Calls, h.second.Calls...) {
		if len(call.Arguments) == 0 {
			continue
		}
		switch req := call.Arguments.Get(len(call.Arguments) - 1).(type) {
		case ramp.OnrampCreateRequest:
			return req.IdempotencyKey
		case ramp.OfframpCreateRequest:
			return req.IdempotencyKey
		}
	}
	return ""
}

// The order exists upstream: a lagging quote bookkeeping still answers it, an
// unrecorded order is an error and is not broadcast.
func TestCreateAfterTheProviderAccepted(t *testing.T) {
	writeFailed := errors.New("pq: connection reset")
	cases := map[string]struct {
		updateErr, consumeErr error
		wantErr               error
	}{
		"quote bookkeeping lags": {consumeErr: writeFailed},
		"order write fails":      {updateErr: writeFailed, wantErr: writeFailed},
	}
	for _, d := range directions {
		for name, c := range cases {
			t.Run(d.name+"/"+name, func(t *testing.T) {
				h := newDirectionHarness(t, d)
				var placed atomic.Int32
				d.upstream(h, &placed)
				h.txRepo.EXPECT().FindByCustomerAndEstimate(userID, d.provider, d.estimate().ID, d.txType).
					Return(nil, nil).Twice()
				h.txRepo.EXPECT().Create(testifymock.Anything).Return(nil).Once()
				h.txRepo.EXPECT().Update(testifymock.Anything).Return(c.updateErr).Once()
				if c.updateErr == nil {
					h.estimateRepo.EXPECT().MarkConsumed(testifymock.Anything).Return(c.consumeErr).Once()
					h.broadcaster.EXPECT().Publish(userID, testifymock.Anything).Once()
				}

				created, err := d.create(h)
				require.ErrorIs(t, err, c.wantErr)
				require.Equal(t, c.wantErr == nil, created)
				require.EqualValues(t, 1, placed.Load())
			})
		}
	}
}

// The lock makes the dedup read and the reservation one step. The negative
// control proves the test sees the race it guards.
func TestConcurrentCreatesOnOneEstimatePlaceOneOrder(t *testing.T) {
	cases := map[string]struct {
		serialize bool
		want      int32
	}{
		"serialized by the lock":              {serialize: true, want: 1},
		"negative control: a lock that isn't": {serialize: false, want: 2},
	}
	for _, d := range directions {
		for name, c := range cases {
			t.Run(d.name+"/"+name, func(t *testing.T) {
				h := newDirectionHarness(t, d)
				pair := race.New()
				h.rebuild(func(deps *ramps.Deps) { deps.Locker = pair.Locker(t, c.serialize) })
				var placed atomic.Int32
				d.upstream(h, &placed)
				// The harness records lookups into a plain slice; the negative
				// control runs two of them at once.
				h.destinations.ExpectedCalls = nil
				h.destinations.EXPECT().FindRef(testifymock.Anything).Return("", nil).Maybe()

				var mu sync.Mutex
				var reserved []models.Transaction
				h.txRepo.EXPECT().FindByCustomerAndEstimate(userID, d.provider, d.estimate().ID, d.txType).
					RunAndReturn(func(uint, string, uint, string) (*models.Transaction, error) {
						mu.Lock()
						defer mu.Unlock()
						if len(reserved) == 0 {
							return nil, nil
						}
						first := reserved[0]
						return &first, nil
					})
				h.txRepo.EXPECT().Create(testifymock.Anything).RunAndReturn(func(tx *models.Transaction) error {
					pair.Write()
					mu.Lock()
					defer mu.Unlock()
					reserved = append(reserved, *tx)
					return nil
				})
				h.txRepo.EXPECT().Update(testifymock.Anything).Return(nil).Maybe()
				h.estimateRepo.EXPECT().MarkConsumed(testifymock.Anything).Return(nil).Maybe()
				h.broadcaster.EXPECT().Publish(userID, testifymock.Anything).Maybe()

				errs := pair.Run(2, func() error {
					_, err := d.create(h)
					return err
				})
				for _, err := range errs {
					require.NoError(t, err)
				}
				require.Equal(t, c.want, placed.Load())
				require.Len(t, reserved, int(c.want))
			})
		}
	}
}

// Two retries of one stale reservation resend its create once: the resume is
// written under the lock, so the second retry reads a reservation in flight.
func TestConcurrentRetriesOfAStaleReservationResendOnce(t *testing.T) {
	cases := map[string]struct {
		serialize bool
		want      int32
	}{
		"serialized by the lock":              {serialize: true, want: 1},
		"negative control: a lock that isn't": {serialize: false, want: 2},
	}
	for _, d := range directions {
		for name, c := range cases {
			t.Run(d.name+"/"+name, func(t *testing.T) {
				h := newDirectionHarness(t, d)
				pair := race.New()
				h.rebuild(func(deps *ramps.Deps) { deps.Locker = pair.Locker(t, c.serialize) })
				var placed atomic.Int32
				d.upstream(h, &placed)
				h.destinations.ExpectedCalls = nil
				h.destinations.EXPECT().FindRef(testifymock.Anything).Return("", nil).Maybe()

				estimate, key, deadline := d.estimate().ID, "reserved-key", time.Now().Add(-time.Minute)
				var mu sync.Mutex
				row := models.Transaction{
					CustomerID: userID,
					UserID:     userID, Provider: d.provider, Type: d.txType, ExternalID: "reserved",
					Status: models.TxStatusPending, EstimateID: &estimate, IdempotencyKey: &key, ExpiresAt: &deadline,
				}
				h.txRepo.EXPECT().FindByCustomerAndEstimate(userID, d.provider, estimate, d.txType).
					RunAndReturn(func(uint, string, uint, string) (*models.Transaction, error) {
						mu.Lock()
						defer mu.Unlock()
						found := row
						return &found, nil
					})
				h.txRepo.EXPECT().Update(testifymock.Anything).RunAndReturn(func(tx *models.Transaction) error {
					if tx.GatewayOrderID == "" {
						pair.Write()
					}
					mu.Lock()
					defer mu.Unlock()
					row = *tx
					return nil
				})
				h.estimateRepo.EXPECT().MarkConsumed(testifymock.Anything).Return(nil).Maybe()
				h.broadcaster.EXPECT().Publish(userID, testifymock.Anything).Maybe()

				errs := pair.Run(2, func() error {
					_, err := d.create(h)
					return err
				})
				for _, err := range errs {
					require.NoError(t, err)
				}
				require.Equal(t, c.want, placed.Load())
				h.txRepo.AssertNotCalled(t, "Create", testifymock.Anything)
			})
		}
	}
}
