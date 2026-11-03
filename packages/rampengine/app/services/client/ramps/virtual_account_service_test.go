package ramps_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/goravel/framework/support/carbon"
	"github.com/goravel/framework/testing/mock"
	"github.com/goravel/framework/testing/utils"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/services/mfa"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
	"github.com/macrowallets/waas/packages/rampengine/tests/support"
	"github.com/macrowallets/waas/packages/rampengine/tests/support/race"
)

// standingAdapter is a provider that serves the rail both ways: the fat
// ProviderAdapter the router resolves, and the standing-account port the
// service type-asserts off it. Mockery generates the two separately because
// they are two interfaces; a real provider implements both on one type, and so
// does this. Code() is stated explicitly because both embeds carry one.
type standingAdapter struct {
	*mocks.MockProviderAdapter
	*mocks.MockVirtualAccountAdapter
}

func (a *standingAdapter) Code() ramp.ProviderCode { return a.MockProviderAdapter.Code() }

// vaHarness wires the router with mocked repositories so the eligibility chain
// — rail order, provider config, integration, endorsements — runs for real. The
// rail is served by two providers: Bridge collects through a standing account,
// Ozav does not implement the port at all.
type vaHarness struct {
	service  *ramps.VirtualAccountService
	deps     ramps.Deps
	standing *standingAdapter
	plain    *mocks.MockProviderAdapter
	settings *mocks.MockSettingsSource
	integ    *mocks.MockCustomerProviderIntegrationRepository
	kycs     *mocks.MockCustomerProviderKycRepository
	accounts *mocks.MockCustomerVirtualAccountRepository
	mfa      *mocks.MockMfaStepUpValidator
	steps    *mocks.MockPendingStepsSource
	// wallets answers the address registry from registered (address →
	// customer).
	wallets    *mocks.MockCustomerWalletRepository
	registered map[string]uint
}

func newVirtualAccountHarness(t *testing.T, providerCodes ...string) *vaHarness {
	t.Helper()
	facades := mock.Factory()
	facades.Log()
	// A company's document digest is keyed by APP_KEY.
	facades.Config().EXPECT().GetString("app.key").Return("test-app-key").Maybe()

	h := &vaHarness{
		standing: &standingAdapter{
			MockProviderAdapter:       mocks.NewMockProviderAdapter(t),
			MockVirtualAccountAdapter: mocks.NewMockVirtualAccountAdapter(t),
		},
		plain:    mocks.NewMockProviderAdapter(t),
		settings: mocks.NewMockSettingsSource(t),
		integ:    mocks.NewMockCustomerProviderIntegrationRepository(t),
		kycs:     mocks.NewMockCustomerProviderKycRepository(t),
		accounts: mocks.NewMockCustomerVirtualAccountRepository(t),
		mfa:      mocks.NewMockMfaStepUpValidator(t),
		steps:    mocks.NewMockPendingStepsSource(t),
		wallets:  mocks.NewMockCustomerWalletRepository(t),

		registered: map[string]uint{},
	}
	h.wallets.EXPECT().FindByAddress(testifymock.Anything, testifymock.Anything).
		RunAndReturn(func(chain, address string) (*models.CustomerWallet, error) {
			return registeredWallet(h.registered, chain, address), nil
		}).Maybe()
	h.standing.MockProviderAdapter.EXPECT().Code().Return(models.ProviderBridge).Maybe()
	h.standing.MockProviderAdapter.EXPECT().Supports(testifymock.Anything).Return(true).Maybe()
	h.plain.EXPECT().Code().Return(models.ProviderOzav).Maybe()
	h.plain.EXPECT().Supports(testifymock.Anything).Return(true).Maybe()

	railGroup := ramp.RailGroup(ramp.RailPixPayin)
	h.settings.EXPECT().String(railGroup, ramp.SettingProviderOrder).
		Return(ramp.FormatProviderOrder(providerCodes), nil).Maybe()
	h.settings.EXPECT().String(railGroup, ramp.FeeSettingKey(ramp.ProviderBridge)).Return("1.75", nil).Maybe()
	h.settings.EXPECT().String(railGroup, ramp.FeeSettingKey(ramp.ProviderOzav)).Return("1.50", nil).Maybe()
	h.settings.EXPECT().String(railGroup, ramp.FeeSettingKey(ramp.ProviderBlindPay)).Return("0.00", nil).Maybe()
	h.settings.EXPECT().String(railGroup, ramp.SettingMinAmountBRL).Return("", nil).Maybe()
	h.settings.EXPECT().String(railGroup, ramp.SettingMinAmountBRLByToken).Return("", nil).Maybe()
	h.settings.EXPECT().String(railGroup, ramp.SettingMaxAmountBRL).Return("", nil).Maybe()
	h.settings.EXPECT().String(railGroup, ramp.SettingOzavDeliveryMaxAttempts).Return("3", nil).Maybe()

	for _, code := range providerCodes {
		h.eligible(t, code)
	}

	config := ramp.NewConfig(h.settings)
	router := ramp.NewRouter(ramp.RouterDeps{
		Factory: ramp.NewFactory(h.standing, h.plain), Config: config,
		Integrations: h.integ, Kyc: h.kycs, Persons: ramp.Persons{Customers: support.IndividualCustomers(t), Profiles: noRetainedProfile(t)}, AppEnv: "local",
	})
	h.deps = ramps.Deps{
		Router: router, Config: config,
		Locker: passthroughLocker{}, Features: openFeatures(),
		VirtualAccounts: h.accounts, PendingSteps: h.steps, Wallets: h.wallets,
	}
	h.service = ramps.NewVirtualAccountService(h.deps, h.mfa)
	return h
}

// rebuild rewires the service after a test swapped one collaborator.
func (h *vaHarness) rebuild(swap func(*ramps.Deps)) {
	swap(&h.deps)
	h.service = ramps.NewVirtualAccountService(h.deps, h.mfa)
}

// withoutMfa lets destination updates through: the user has MFA off, so
// step-up does not ask for a code.
func (h *vaHarness) withoutMfa() {
	h.mfa.EXPECT().Enrolled(testifymock.Anything).Return(false, nil).Maybe()
}

// eligible makes the provider pass the whole chain for this user.
func (h *vaHarness) eligible(t *testing.T, code string) {
	t.Helper()
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
	}))
	h.kycs.EXPECT().FindByCustomerAndProvider(userID, code).Return(approved, nil).Maybe()
}

// withoutIntegration drops the user's link to every provider.
func (h *vaHarness) withoutIntegration() {
	h.integ.ExpectedCalls = nil
	h.integ.EXPECT().FindByCustomerAndProvider(userID, testifymock.Anything).Return(nil, nil).Maybe()
}

// withoutEndorsement leaves the link in place but closes the rail.
func (h *vaHarness) withoutEndorsement(t *testing.T) {
	t.Helper()
	pending := &models.CustomerProviderKyc{CustomerID: userID, KycStatus: models.KycStatusUnderReview}
	require.NoError(t, pending.SetEndorsements(nil))
	h.kycs.ExpectedCalls = nil
	h.kycs.EXPECT().FindByCustomerAndProvider(userID, testifymock.Anything).Return(pending, nil).Maybe()
}

func provisionInput() dto.ProvisionVirtualAccountDTO {
	return dto.ProvisionVirtualAccountDTO{
		SourceCurrency: ramps.ClientSourceCurrency,
		Token:          "USDC", Chain: "solana", Wallet: wallet,
	}
}

// bridgeVirtualAccount is what the adapter answers with.
func bridgeVirtualAccount() *ramp.VirtualAccount {
	return &ramp.VirtualAccount{
		ProviderAccountID: "va_1",
		Status:            models.VirtualAccountStatusActive,
		DepositInstructions: map[string]any{
			"br_code": "00020126770014br.gov.bcb.pix", "account_holder_name": "XIP",
		},
		Destination:    ramp.CryptoLeg{Currency: "usdc", PaymentRail: "solana", Address: wallet},
		SourceCurrency: ramps.ClientSourceCurrency,
		FeePercent:     "1.50",
		Raw:            []byte(`{"id":"va_1"}`),
	}
}

// storedAccount is a row the user already has.
func storedAccount(provider string) *models.CustomerVirtualAccount {
	return &models.CustomerVirtualAccount{
		CustomerID: userID,
		UserID:     userID, Provider: provider, ExternalAccountID: "va_existing",
		SourceCurrency: ramps.ClientSourceCurrency, Status: models.VirtualAccountStatusActive,
		DestinationCurrency: "usdc", DestinationChain: "solana", DestinationAddress: wallet,
		FeePercent:          support.Ptr("1.50"),
		DepositInstructions: `{"br_code":"00020126SAVED"}`,
		RawPayload:          `{"id":"va_existing"}`,
	}
}

// --- provision --------------------------------------------------------------

func TestProvisionCreatesTheAccountWhenTheUserHasNone(t *testing.T) {
	h := newVirtualAccountHarness(t, models.ProviderBridge)

	h.accounts.EXPECT().FindActiveByCustomerAndCurrency(userID, ramps.ClientSourceCurrency).
		Return(nil, nil).Times(3)
	h.accounts.EXPECT().FindProvisioningByCustomerAndCurrency(userID, ramps.ClientSourceCurrency).
		Return(nil, nil).Once()
	h.accounts.EXPECT().Create(testifymock.Anything).Return(nil).Once().
		Run(func(args testifymock.Arguments) {
			args.Get(0).(*models.CustomerVirtualAccount).ID = 99
		})
	h.standing.MockVirtualAccountAdapter.EXPECT().
		ProvisionVirtualAccount(testifymock.Anything, testifymock.Anything,
			testifymock.MatchedBy(func(req ramp.VirtualAccountRequest) bool {
				return req.FeePercent == "1.75" &&
					req.SourceCurrency == ramps.ClientSourceCurrency &&
					req.Destination.Address == wallet &&
					req.IdempotencyKey != ""
			})).
		Return(bridgeVirtualAccount(), nil).Once()

	var persisted *models.CustomerVirtualAccount
	h.accounts.EXPECT().FindByID(uint(99)).RunAndReturn(func(id uint) (*models.CustomerVirtualAccount, error) {
		return &models.CustomerVirtualAccount{
			CustomerID: userID,
			UserID:     userID, Provider: models.ProviderBridge,
			ExternalAccountID:   "provisioning:1:brl",
			SourceCurrency:      ramps.ClientSourceCurrency,
			Status:              models.VirtualAccountStatusProvisioning,
			DestinationCurrency: "usdc", DestinationChain: "solana", DestinationAddress: wallet,
			DepositInstructions: "{}", RawPayload: "{}",
		}, nil
	}).Once()
	h.accounts.EXPECT().Update(testifymock.Anything).Return(nil).Once().
		Run(func(args testifymock.Arguments) {
			persisted = args.Get(0).(*models.CustomerVirtualAccount)
		})

	view, created, err := h.service.Provision(context.Background(), user, person, provisionInput())
	require.NoError(t, err)
	require.True(t, created)

	require.Equal(t, models.ProviderBridge, persisted.Provider)
	require.Equal(t, "va_1", persisted.ExternalAccountID)
	require.Equal(t, models.VirtualAccountStatusActive, persisted.Status)
	require.Equal(t, wallet, persisted.DestinationAddress)
	require.JSONEq(t, `{"id":"va_1"}`, persisted.RawPayload)
	require.Equal(t, "00020126770014br.gov.bcb.pix", view.DepositInstructions["br_code"])
	require.Equal(t, "1.50", view.FeePercent)
}

// The deposit code is permanent and the user has it saved. A second call is a
// read, never a second provisioning.
func TestProvisionReturnsTheExistingAccountWithoutCallingTheProvider(t *testing.T) {
	h := newVirtualAccountHarness(t, models.ProviderBridge)
	h.accounts.EXPECT().FindActiveByCustomerAndCurrency(userID, ramps.ClientSourceCurrency).
		Return(storedAccount(models.ProviderBridge), nil).Once()

	view, created, err := h.service.Provision(context.Background(), user, person, provisionInput())
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, "00020126SAVED", view.DepositInstructions["br_code"])

	h.standing.MockVirtualAccountAdapter.AssertNotCalled(t, "ProvisionVirtualAccount",
		testifymock.Anything, testifymock.Anything, testifymock.Anything)
	h.accounts.AssertNotCalled(t, "Create", testifymock.Anything)
}

// reservation is a provisioning row taken at reservedAt.
func reservation(id uint, reservedAt time.Time) *models.CustomerVirtualAccount {
	row := &models.CustomerVirtualAccount{
		CustomerID: userID,
		UserID:     userID, Provider: models.ProviderBridge,
		ExternalAccountID:   "provisioning:1:brl",
		SourceCurrency:      ramps.ClientSourceCurrency,
		Status:              models.VirtualAccountStatusProvisioning,
		DepositInstructions: "{}", RawPayload: "{}",
	}
	row.ID = id
	row.CreatedAt = carbon.NewDateTime(carbon.FromStdTime(reservedAt))
	return row
}

// A concurrent provision that finds someone else's reservation must not call
// the provider — hitchhiking is how one user ends up with two deposit codes.
// The client gets ErrProvisioningInFlight (409) and re-enters.
func TestProvisionRefusesWhenAnotherRequestHoldsTheReservation(t *testing.T) {
	h := newVirtualAccountHarness(t, models.ProviderBridge)

	h.accounts.EXPECT().FindActiveByCustomerAndCurrency(userID, ramps.ClientSourceCurrency).
		Return(nil, nil).Twice()
	h.accounts.EXPECT().FindProvisioningByCustomerAndCurrency(userID, ramps.ClientSourceCurrency).
		Return(reservation(42, time.Now()), nil).Once()

	_, created, err := h.service.Provision(context.Background(), user, person, provisionInput())
	require.ErrorIs(t, err, ramps.ErrProvisioningInFlight)
	require.False(t, created)

	h.standing.MockVirtualAccountAdapter.AssertNotCalled(t, "ProvisionVirtualAccount",
		testifymock.Anything, testifymock.Anything, testifymock.Anything)
	h.accounts.AssertNotCalled(t, "Create", testifymock.Anything)
}

// A reservation whose request died mid-call (the 3 s request timeout, a
// crash) would otherwise answer 409 forever. Past the window it is replaced,
// and the provider call it guarded is safe to repeat: the key is derived.
func TestProvisionReplacesAReservationPastTheWindow(t *testing.T) {
	h := newVirtualAccountHarness(t, models.ProviderBridge)
	stale := reservation(42, time.Now().Add(-time.Hour))

	h.accounts.EXPECT().FindActiveByCustomerAndCurrency(userID, ramps.ClientSourceCurrency).
		Return(nil, nil).Times(3)
	h.accounts.EXPECT().FindProvisioningByCustomerAndCurrency(userID, ramps.ClientSourceCurrency).
		Return(stale, nil).Once()
	h.accounts.EXPECT().Delete(stale).Return(nil).Once()
	h.accounts.EXPECT().Create(testifymock.Anything).Return(nil).Once().
		Run(func(args testifymock.Arguments) { args.Get(0).(*models.CustomerVirtualAccount).ID = 99 })
	h.standing.MockVirtualAccountAdapter.EXPECT().
		ProvisionVirtualAccount(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(bridgeVirtualAccount(), nil).Once()
	h.accounts.EXPECT().FindByID(uint(99)).Return(reservation(99, time.Now()), nil).Once()
	h.accounts.EXPECT().Update(testifymock.Anything).Return(nil).Once()

	_, created, err := h.service.Provision(context.Background(), user, person, provisionInput())
	require.NoError(t, err)
	require.True(t, created)
}

// Stickiness. Failing over an order is invisible to the user; failing over a
// standing account hands them a new deposit code for one they already saved in
// their bank app. The rail order decides where *new* accounts are provisioned
// and never touches an existing one.
func TestProvisionKeepsTheAccountItsProviderAlreadyGave(t *testing.T) {
	// The rail now heads with Bridge, but the user's account is at Ozav.
	h := newVirtualAccountHarness(t, models.ProviderBridge)
	h.accounts.EXPECT().FindActiveByCustomerAndCurrency(userID, ramps.ClientSourceCurrency).
		Return(storedAccount(models.ProviderOzav), nil).Once()

	view, created, err := h.service.Provision(context.Background(), user, person, provisionInput())
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, "00020126SAVED", view.DepositInstructions["br_code"],
		"the deposit code the user saved must survive a change to the rail order")

	h.standing.MockVirtualAccountAdapter.AssertNotCalled(t, "ProvisionVirtualAccount",
		testifymock.Anything, testifymock.Anything, testifymock.Anything)
	h.accounts.AssertNotCalled(t, "Create", testifymock.Anything)
}

// The reason the service resolves with a call instead of Resolve: Resolve
// returns candidates[0], so a rail headed by a provider without the port would
// report no_provider_available while Bridge sat behind it ready to serve.
func TestProvisionSkipsAProviderThatDoesNotCollectThroughAStandingAccount(t *testing.T) {
	h := newVirtualAccountHarness(t, models.ProviderOzav, models.ProviderBridge)

	h.accounts.EXPECT().FindActiveByCustomerAndCurrency(userID, ramps.ClientSourceCurrency).
		Return(nil, nil).Times(3)
	h.accounts.EXPECT().FindProvisioningByCustomerAndCurrency(userID, ramps.ClientSourceCurrency).
		Return(nil, nil).Once()
	h.standing.MockVirtualAccountAdapter.EXPECT().
		ProvisionVirtualAccount(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(bridgeVirtualAccount(), nil).Once()

	var persisted *models.CustomerVirtualAccount
	h.accounts.EXPECT().Create(testifymock.Anything).Return(nil).Once().
		Run(func(args testifymock.Arguments) {
			args.Get(0).(*models.CustomerVirtualAccount).ID = 99
		})
	h.accounts.EXPECT().FindByID(uint(99)).RunAndReturn(func(id uint) (*models.CustomerVirtualAccount, error) {
		row := &models.CustomerVirtualAccount{
			CustomerID: userID,
			UserID:     userID, Provider: models.ProviderBridge,
			ExternalAccountID:   "provisioning:1:brl",
			SourceCurrency:      ramps.ClientSourceCurrency,
			Status:              models.VirtualAccountStatusProvisioning,
			DepositInstructions: "{}", RawPayload: "{}",
		}
		row.ID = id
		return row, nil
	}).Once()
	h.accounts.EXPECT().Update(testifymock.Anything).Return(nil).Once().
		Run(func(args testifymock.Arguments) {
			persisted = args.Get(0).(*models.CustomerVirtualAccount)
		})

	_, created, err := h.service.Provision(context.Background(), user, person, provisionInput())
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, models.ProviderBridge, persisted.Provider,
		"the account is recorded against the provider that actually served it")
}

// A provisioning call that failed may still have created the account upstream.
// Trying the next provider is how one user ends up with two deposit codes.
func TestProvisionDoesNotFailOverAfterAFailedProvision(t *testing.T) {
	h := newVirtualAccountHarness(t, models.ProviderBridge, models.ProviderOzav)

	h.accounts.EXPECT().FindActiveByCustomerAndCurrency(userID, ramps.ClientSourceCurrency).
		Return(nil, nil).Twice()
	h.accounts.EXPECT().FindProvisioningByCustomerAndCurrency(userID, ramps.ClientSourceCurrency).
		Return(nil, nil).Once()
	h.standing.MockVirtualAccountAdapter.EXPECT().
		ProvisionVirtualAccount(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(nil, errors.New("bridge: POST /virtual_accounts: context deadline exceeded")).Once()
	h.accounts.EXPECT().Create(testifymock.Anything).Return(nil).Once().
		Run(func(args testifymock.Arguments) {
			args.Get(0).(*models.CustomerVirtualAccount).ID = 99
		})

	_, _, err := h.service.Provision(context.Background(), user, person, provisionInput())
	require.Error(t, err)
	h.accounts.AssertNotCalled(t, "Update", testifymock.Anything)
}

// Every provider on the rail collects through per-order instructions, so the
// rail cannot be served this way at all. That is a routing outcome, not an
// upstream failure — the client is told nobody serves it.
func TestProvisionRefusesWhenNoProviderCollectsThroughAStandingAccount(t *testing.T) {
	h := newVirtualAccountHarness(t, models.ProviderOzav)
	h.accounts.EXPECT().FindActiveByCustomerAndCurrency(userID, ramps.ClientSourceCurrency).
		Return(nil, nil).Twice()
	h.accounts.EXPECT().FindProvisioningByCustomerAndCurrency(userID, ramps.ClientSourceCurrency).
		Return(nil, nil).Once()

	_, _, err := h.service.Provision(context.Background(), user, person, provisionInput())
	require.ErrorIs(t, err, ramp.ErrNoProviderAvailable)
	h.accounts.AssertNotCalled(t, "Create", testifymock.Anything)
}

func TestProvisionRefusesWithoutAnIntegration(t *testing.T) {
	h := newVirtualAccountHarness(t, models.ProviderBridge)
	h.withoutIntegration()
	h.accounts.EXPECT().FindActiveByCustomerAndCurrency(userID, ramps.ClientSourceCurrency).
		Return(nil, nil).Twice()
	h.accounts.EXPECT().FindProvisioningByCustomerAndCurrency(userID, ramps.ClientSourceCurrency).
		Return(nil, nil).Once()

	_, _, err := h.service.Provision(context.Background(), user, person, provisionInput())
	require.ErrorIs(t, err, ramp.ErrIntegrationRequired)
}

func TestProvisionRefusesWhileTheEndorsementIsPending(t *testing.T) {
	h := newVirtualAccountHarness(t, models.ProviderBridge)
	h.withoutEndorsement(t)
	h.accounts.EXPECT().FindActiveByCustomerAndCurrency(userID, ramps.ClientSourceCurrency).
		Return(nil, nil).Twice()
	h.accounts.EXPECT().FindProvisioningByCustomerAndCurrency(userID, ramps.ClientSourceCurrency).
		Return(nil, nil).Once()
	h.steps.EXPECT().PendingSteps(userID, models.ProviderBridge).
		Return([]string{"proof_of_address"}).Once()

	_, _, err := h.service.Provision(context.Background(), user, person, provisionInput())
	require.ErrorIs(t, err, ramp.ErrKycPending)

	// The standing account is where most users meet a KYC refusal, so it names
	// the provider and the steps just as an estimate does.
	var blocked *ramp.KycBlocked
	require.ErrorAs(t, err, &blocked)
	require.Equal(t, models.ProviderBridge, blocked.Provider)
	require.Equal(t, []string{"proof_of_address"}, blocked.PendingSteps)
}

// A currency no rail collects is refused, never assumed onto a default rail.
func TestProvisionRefusesACurrencyNoRailCollects(t *testing.T) {
	h := newVirtualAccountHarness(t, models.ProviderBridge)

	in := provisionInput()
	in.SourceCurrency = "eur"
	h.accounts.EXPECT().FindActiveByCustomerAndCurrency(userID, "eur").Return(nil, nil).Once()

	_, _, err := h.service.Provision(context.Background(), user, person, in)
	require.ErrorIs(t, err, ramp.ErrNoProviderAvailable)
	h.standing.MockVirtualAccountAdapter.AssertNotCalled(t, "ProvisionVirtualAccount",
		testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// --- show -------------------------------------------------------------------

func TestShowVirtualAccountReturnsTheStandingAccount(t *testing.T) {
	h := newVirtualAccountHarness(t, models.ProviderBridge)
	h.accounts.EXPECT().FindActiveByCustomerAndCurrency(userID, ramps.ClientSourceCurrency).
		Return(storedAccount(models.ProviderBridge), nil).Once()

	view, err := h.service.Show(userID, ramps.ClientSourceCurrency)
	require.NoError(t, err)
	require.Equal(t, models.VirtualAccountStatusActive, view.Status)
	require.Equal(t, "USDC", view.Token)
	require.Equal(t, wallet, view.Wallet)
}

func TestShowVirtualAccountReportsNoAccount(t *testing.T) {
	h := newVirtualAccountHarness(t, models.ProviderBridge)
	h.accounts.EXPECT().FindActiveByCustomerAndCurrency(userID, ramps.ClientSourceCurrency).
		Return(nil, nil).Once()

	_, err := h.service.Show(userID, ramps.ClientSourceCurrency)
	require.ErrorIs(t, err, ramps.ErrVirtualAccountNotFound)
}

// --- update -----------------------------------------------------------------

// Changing the destination is a PUT on the account, never a new one: the
// deposit instructions survive it (verified), and whatever the provider answers
// with is what gets persisted.
func TestUpdateDestinationGoesToTheAccountsOwnProvider(t *testing.T) {
	h := newVirtualAccountHarness(t, models.ProviderBridge)
	h.withoutMfa()
	existing := storedAccount(models.ProviderBridge)
	h.accounts.EXPECT().FindActiveByCustomerAndCurrency(userID, ramps.ClientSourceCurrency).
		Return(existing, nil).Once()

	updated := bridgeVirtualAccount()
	updated.ProviderAccountID = existing.ExternalAccountID
	updated.Destination.Address = "NewWalletAddress"
	h.standing.MockVirtualAccountAdapter.EXPECT().
		UpdateVirtualAccount(testifymock.Anything, testifymock.Anything, "va_existing",
			testifymock.MatchedBy(func(req ramp.VirtualAccountUpdate) bool {
				return req.Destination != nil && req.Destination.Address == "NewWalletAddress" &&
					req.Active == nil
			})).
		Return(updated, nil).Once()

	var saved *models.CustomerVirtualAccount
	h.accounts.EXPECT().Update(testifymock.Anything).Return(nil).Once().
		Run(func(args testifymock.Arguments) {
			saved = args.Get(0).(*models.CustomerVirtualAccount)
		})

	view, err := h.service.UpdateDestination(context.Background(), user, person, dto.UpdateVirtualAccountDTO{
		ProvisionVirtualAccountDTO: dto.ProvisionVirtualAccountDTO{
			SourceCurrency: ramps.ClientSourceCurrency,
			Token:          "USDC", Chain: "solana", Wallet: "NewWalletAddress",
		},
	})
	require.NoError(t, err)
	require.Equal(t, "NewWalletAddress", saved.DestinationAddress)
	require.Equal(t, "NewWalletAddress", view.Wallet)
	require.Equal(t, "00020126770014br.gov.bcb.pix", view.DepositInstructions["br_code"],
		"the deposit code survives a destination change")
}

// The standing account's br_code does not rotate on a destination change, so
// step-up runs before anything else — including loading the account.
func TestUpdateDestinationRequiresMfaStepUp(t *testing.T) {
	h := newVirtualAccountHarness(t, models.ProviderBridge)
	h.mfa.EXPECT().Enrolled(user).Return(true, nil).Once()

	_, err := h.service.UpdateDestination(context.Background(), user, person, dto.UpdateVirtualAccountDTO{
		ProvisionVirtualAccountDTO: dto.ProvisionVirtualAccountDTO{
			SourceCurrency: ramps.ClientSourceCurrency,
			Token:          "USDC", Chain: "solana", Wallet: "NewWalletAddress",
		},
	})
	require.ErrorIs(t, err, ramp.ErrMfaStepUpRequired)
	h.accounts.AssertNotCalled(t, "FindActiveByCustomerAndCurrency", testifymock.Anything, testifymock.Anything)
}

func TestUpdateDestinationWithoutAnAccountIsNotFound(t *testing.T) {
	h := newVirtualAccountHarness(t, models.ProviderBridge)
	h.withoutMfa()
	h.accounts.EXPECT().FindActiveByCustomerAndCurrency(userID, ramps.ClientSourceCurrency).
		Return(nil, nil).Once()

	_, err := h.service.UpdateDestination(context.Background(), user, person, dto.UpdateVirtualAccountDTO{
		ProvisionVirtualAccountDTO: dto.ProvisionVirtualAccountDTO{
			SourceCurrency: ramps.ClientSourceCurrency,
			Token:          "USDC", Chain: "solana", Wallet: "NewWalletAddress",
		},
	})
	require.ErrorIs(t, err, ramps.ErrVirtualAccountNotFound)
}

// --- provision: the reservation ----------------------------------------------

// The gate stops new business and nothing else. Provision is read-then-create
// and only the create half is a birth: the deposit code a user already saved in
// their bank app is still theirs to see while the rail is closed.
func TestProvisionOnAClosedRail(t *testing.T) {
	cases := map[string]struct {
		held    *models.CustomerVirtualAccount
		wantErr error
	}{
		"the user holds an account": {held: storedAccount(models.ProviderBridge)},
		"the user has none":         {wantErr: ramp.ErrRailDisabled},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := newVirtualAccountHarness(t, models.ProviderBridge)
			features := mocks.NewMockFeature(t)
			features.EXPECT().IsActive(testifymock.Anything, ramp.RailPixPayin.Flag(), models.UserFlagScope(userID)).
				Return(false, nil).Maybe()
			h.rebuild(func(d *ramps.Deps) { d.Features = features })
			h.accounts.EXPECT().FindActiveByCustomerAndCurrency(userID, ramps.ClientSourceCurrency).
				Return(c.held, nil).Once()

			view, _, err := h.service.Provision(context.Background(), user, person, provisionInput())
			require.ErrorIs(t, err, c.wantErr)
			if c.held != nil {
				require.Equal(t, "00020126SAVED", view.DepositInstructions["br_code"])
			}
			h.accounts.AssertNotCalled(t, "Create", testifymock.Anything)
		})
	}
}

// The request that reserved is the only one that talks to the provider, so a
// reservation that could not be written means no provider call at all.
func TestProvisionCallsNoProviderWhenTheReservationIsNotWritten(t *testing.T) {
	h := newVirtualAccountHarness(t, models.ProviderBridge)
	h.accounts.EXPECT().FindActiveByCustomerAndCurrency(userID, ramps.ClientSourceCurrency).Return(nil, nil).Twice()
	h.accounts.EXPECT().FindProvisioningByCustomerAndCurrency(userID, ramps.ClientSourceCurrency).Return(nil, nil).Once()
	writeFailed := errors.New("pq: duplicate key value violates unique constraint")
	h.accounts.EXPECT().Create(testifymock.Anything).Return(writeFailed).Once()

	_, _, err := h.service.Provision(context.Background(), user, person, provisionInput())
	require.ErrorIs(t, err, writeFailed)
	h.standing.MockVirtualAccountAdapter.AssertNotCalled(t, "ProvisionVirtualAccount",
		testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// A reservation is released only when the provision provably never left, so the
// user can try again. Any other failure is unknown state — the provider may
// hold the account — and the row stays to keep a second create out.
func TestProvisionReleasesTheReservationOnlyWhenTheRequestNeverLeft(t *testing.T) {
	notSent := fmt.Errorf("bridge: POST /virtual_accounts: %w: connection refused", ramp.ErrRequestNotSent)
	cases := map[string]struct {
		providerErr error
		releaseErr  error
		released    bool
	}{
		"never sent":                {providerErr: notSent, released: true},
		"never sent, release fails": {providerErr: notSent, releaseErr: errors.New("pq: connection reset"), released: true},
		"outcome unknown":           {providerErr: errors.New("bridge: POST /virtual_accounts: context deadline exceeded")},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := newVirtualAccountHarness(t, models.ProviderBridge)
			h.accounts.EXPECT().FindActiveByCustomerAndCurrency(userID, ramps.ClientSourceCurrency).Return(nil, nil).Twice()
			h.accounts.EXPECT().FindProvisioningByCustomerAndCurrency(userID, ramps.ClientSourceCurrency).Return(nil, nil).Once()
			var reservation *models.CustomerVirtualAccount
			h.accounts.EXPECT().Create(testifymock.Anything).Return(nil).Once().
				Run(func(args testifymock.Arguments) { reservation = args.Get(0).(*models.CustomerVirtualAccount) })
			h.standing.MockVirtualAccountAdapter.EXPECT().
				ProvisionVirtualAccount(testifymock.Anything, testifymock.Anything, testifymock.Anything).
				Return(nil, c.providerErr).Once()
			if c.released {
				h.accounts.EXPECT().Delete(testifymock.MatchedBy(func(row *models.CustomerVirtualAccount) bool {
					return row == reservation
				})).Return(c.releaseErr).Once()
			}

			_, _, err := h.service.Provision(context.Background(), user, person, provisionInput())
			require.ErrorIs(t, err, c.providerErr)
			if !c.released {
				h.accounts.AssertNotCalled(t, "Delete", testifymock.Anything)
			}
		})
	}
}

// An operator may release the reservation while the provider is called, and an
// account someone else activated is the answer rather than a second one.
func TestProvisionFinalize(t *testing.T) {
	writeFailed := errors.New("pq: connection reset")
	reservation := func() *models.CustomerVirtualAccount {
		row := storedAccount(models.ProviderBridge)
		row.ID, row.Status = 99, models.VirtualAccountStatusProvisioning
		return row
	}
	cases := map[string]struct {
		active      *models.CustomerVirtualAccount
		current     *models.CustomerVirtualAccount
		updateErr   error
		wantCreated bool
		wantErr     bool
	}{
		"activated by someone else first": {active: storedAccount(models.ProviderBridge)},
		"reservation released meanwhile":  {wantErr: true},
		"final write fails":               {current: reservation(), updateErr: writeFailed, wantErr: true},
		"merged onto the reservation":     {current: reservation(), wantCreated: true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := newVirtualAccountHarness(t, models.ProviderBridge)
			h.accounts.EXPECT().FindActiveByCustomerAndCurrency(userID, ramps.ClientSourceCurrency).Return(nil, nil).Twice()
			h.accounts.EXPECT().FindProvisioningByCustomerAndCurrency(userID, ramps.ClientSourceCurrency).Return(nil, nil).Once()
			h.accounts.EXPECT().Create(testifymock.Anything).Return(nil).Once().
				Run(func(args testifymock.Arguments) { args.Get(0).(*models.CustomerVirtualAccount).ID = 99 })
			h.standing.MockVirtualAccountAdapter.EXPECT().
				ProvisionVirtualAccount(testifymock.Anything, testifymock.Anything, testifymock.Anything).
				Return(bridgeVirtualAccount(), nil).Once()
			h.accounts.EXPECT().FindActiveByCustomerAndCurrency(userID, ramps.ClientSourceCurrency).Return(c.active, nil).Once()
			if c.active == nil {
				h.accounts.EXPECT().FindByID(uint(99)).Return(c.current, nil).Once()
			}
			if c.current != nil {
				h.accounts.EXPECT().Update(testifymock.Anything).Return(c.updateErr).Once()
			}

			view, created, err := h.service.Provision(context.Background(), user, person, provisionInput())
			require.Equal(t, c.wantErr, err != nil, "err = %v", err)
			require.Equal(t, c.wantCreated, created)
			if c.active != nil {
				require.Equal(t, "00020126SAVED", view.DepositInstructions["br_code"])
			}
		})
	}
}

// Bridge does not deduplicate virtual accounts: the lock is all that stands
// between two taps and two deposit codes. The negative control proves the test
// sees the race it guards.
func TestConcurrentProvisionsCallTheProviderOnce(t *testing.T) {
	cases := map[string]struct {
		serialize bool
		want      int32
	}{
		"serialized by the lock":              {serialize: true, want: 1},
		"negative control: a lock that isn't": {serialize: false, want: 2},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := newVirtualAccountHarness(t, models.ProviderBridge)
			// Goravel's test log writes every With into one unguarded map, and
			// the negative control resolves the router from two goroutines.
			mock.Factory().App().On("MakeLog").Return(utils.NewTestLog)
			pair := race.New()
			h.rebuild(func(d *ramps.Deps) { d.Locker = pair.Locker(t, c.serialize) })
			rows := h.statefulAccounts(pair)
			var provisioned atomic.Int32
			h.standing.MockVirtualAccountAdapter.EXPECT().
				ProvisionVirtualAccount(testifymock.Anything, testifymock.Anything, testifymock.Anything).
				RunAndReturn(func(context.Context, ramp.CustomerRef, ramp.VirtualAccountRequest) (*ramp.VirtualAccount, error) {
					provisioned.Add(1)
					return bridgeVirtualAccount(), nil
				})

			errs := pair.Run(2, func() error {
				_, _, err := h.service.Provision(context.Background(), user, person, provisionInput())
				return err
			})
			for _, err := range errs {
				if err != nil {
					require.ErrorIs(t, err, ramps.ErrProvisioningInFlight)
				}
			}
			require.Equal(t, c.want, provisioned.Load())
			require.Len(t, *rows, int(c.want))
		})
	}
}

// statefulAccounts backs the account repository with rows in memory, holding
// every reservation write at the race's contention point.
func (h *vaHarness) statefulAccounts(pair *race.Race) *[]models.CustomerVirtualAccount {
	var mu sync.Mutex
	var rows []models.CustomerVirtualAccount
	find := func(status string) (*models.CustomerVirtualAccount, error) {
		mu.Lock()
		defer mu.Unlock()
		for _, row := range rows {
			if row.Status == status {
				return &row, nil
			}
		}
		return nil, nil
	}
	h.accounts.EXPECT().FindActiveByCustomerAndCurrency(userID, ramps.ClientSourceCurrency).
		RunAndReturn(func(uint, string) (*models.CustomerVirtualAccount, error) {
			return find(models.VirtualAccountStatusActive)
		})
	h.accounts.EXPECT().FindProvisioningByCustomerAndCurrency(userID, ramps.ClientSourceCurrency).
		RunAndReturn(func(uint, string) (*models.CustomerVirtualAccount, error) {
			return find(models.VirtualAccountStatusProvisioning)
		}).Maybe()
	h.accounts.EXPECT().Create(testifymock.Anything).RunAndReturn(func(row *models.CustomerVirtualAccount) error {
		pair.Write()
		mu.Lock()
		defer mu.Unlock()
		row.ID = uint(len(rows) + 1)
		rows = append(rows, *row)
		return nil
	})
	h.accounts.EXPECT().FindByID(testifymock.Anything).RunAndReturn(func(id uint) (*models.CustomerVirtualAccount, error) {
		mu.Lock()
		defer mu.Unlock()
		row := rows[id-1]
		return &row, nil
	}).Maybe()
	h.accounts.EXPECT().Update(testifymock.Anything).RunAndReturn(func(row *models.CustomerVirtualAccount) error {
		mu.Lock()
		defer mu.Unlock()
		rows[row.ID-1] = *row
		return nil
	}).Maybe()
	return &rows
}

// --- update: failures ---------------------------------------------------------

// The step-up guards a silent redirect of every future deposit. It fails
// closed when the enrollment cannot be read, answers a wrong code with the
// validator's own error, and lets a right one through to the account.
func TestUpdateDestinationStepUp(t *testing.T) {
	unreadable := errors.New("mfa: load enrollment: connection reset")
	cases := map[string]struct {
		enrolled  bool
		lookupErr error
		codeErr   error
		want      error
	}{
		"enrollment unreadable": {lookupErr: unreadable, want: unreadable},
		"wrong code":            {enrolled: true, codeErr: mfa.ErrInvalidCode, want: mfa.ErrInvalidCode},
		"right code":            {enrolled: true, want: ramps.ErrVirtualAccountNotFound},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := newVirtualAccountHarness(t, models.ProviderBridge)
			h.mfa.EXPECT().Enrolled(user).Return(c.enrolled, c.lookupErr).Once()
			if c.enrolled {
				h.mfa.EXPECT().ValidateStepUpCode(testifymock.Anything, user, "123456", "totp").Return(c.codeErr).Once()
			}
			if c.want == ramps.ErrVirtualAccountNotFound {
				h.accounts.EXPECT().FindActiveByCustomerAndCurrency(userID, ramps.ClientSourceCurrency).Return(nil, nil).Once()
			}

			_, err := h.service.UpdateDestination(context.Background(), user, person, updateInput("NewWalletAddress", "123456"))
			require.ErrorIs(t, err, c.want)
		})
	}
}

// The provider decides what the account now is, so nothing is written locally
// unless it answered.
func TestUpdateDestinationWritesNothingTheProviderRefused(t *testing.T) {
	h := newVirtualAccountHarness(t, models.ProviderBridge)
	h.withoutMfa()
	h.accounts.EXPECT().FindActiveByCustomerAndCurrency(userID, ramps.ClientSourceCurrency).
		Return(storedAccount(models.ProviderBridge), nil).Once()
	refused := errors.New("bridge: PUT /virtual_accounts/va_existing: 422")
	h.standing.MockVirtualAccountAdapter.EXPECT().
		UpdateVirtualAccount(testifymock.Anything, testifymock.Anything, "va_existing", testifymock.Anything).
		Return(nil, refused).Once()

	_, err := h.service.UpdateDestination(context.Background(), user, person, updateInput("NewWalletAddress", ""))
	require.ErrorIs(t, err, refused)
	h.accounts.AssertNotCalled(t, "Update", testifymock.Anything)
}

func updateInput(wallet, code string) dto.UpdateVirtualAccountDTO {
	return dto.UpdateVirtualAccountDTO{
		ProvisionVirtualAccountDTO: dto.ProvisionVirtualAccountDTO{
			SourceCurrency: ramps.ClientSourceCurrency,
			Token:          "USDC", Chain: "solana", Wallet: wallet,
		},
		Code: code, CodeType: "totp",
	}
}
