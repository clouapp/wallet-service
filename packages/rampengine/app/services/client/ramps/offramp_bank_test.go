package ramps_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	mockscrypt "github.com/goravel/framework/mocks/crypt"
	"github.com/goravel/framework/testing/mock"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/bank"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
	"github.com/macrowallets/waas/packages/rampengine/tests/support"
)

const (
	beneficiaryID         = "bnf_ext_1"
	blindpayBankAccountID = "ba_000000000001"
	blindpayCusID         = "cu_000000000001"
)

// limitedAdapter is a provider that also reads the customer's remaining payout
// allowance — the optional port the service type-asserts for.
type limitedAdapter struct {
	*mocks.MockProviderAdapter
	*mocks.MockPayoutLimitReader
}

// bankHarness wires a real router over the bank rail, BlindPay first and
// Bridge second, so the no-failover rule is exercised against a provider that
// would otherwise be tried next.
type bankHarness struct {
	blindpay     *mocks.MockProviderAdapter
	limits       *mocks.MockPayoutLimitReader
	bridge       *mocks.MockProviderAdapter
	accounts     *mocks.MockBankAccountEnsurer
	estimateRepo *mocks.MockRampEstimateRepository
	mfa          *mocks.MockMfaStepUpValidator
	// crypt is facades.Crypt(), which a bank create seals its action with.
	crypt   *mockscrypt.Crypt
	offramp *ramps.OfframpService
	// flagOn answers the bank payout switch; minUSD/maxUSD the rail's bounds
	// (blank is unbounded). Fields read at call time.
	flagOn         bool
	minUSD, maxUSD string
	// registered is the wallet registry a business's money rules read
	// (address → customer).
	registered map[string]uint
	deps       ramps.Deps
}

// newBankHarness builds the harness. withReader decides whether BlindPay
// implements ramp.PayoutLimitReader.
func newBankHarness(t *testing.T, rail ramp.Rail, withReader bool) *bankHarness {
	t.Helper()
	return buildBankHarness(t, rail, func(h *bankHarness) ramp.ProviderAdapter {
		if withReader {
			return &limitedAdapter{MockProviderAdapter: h.blindpay, MockPayoutLimitReader: h.limits}
		}
		return h.blindpay
	})
}

// buildBankHarness builds the harness with first as the adapter BlindPay is
// served by: the optional ports it implements are the test's to choose.
func buildBankHarness(t *testing.T, rail ramp.Rail, first func(*bankHarness) ramp.ProviderAdapter) *bankHarness {
	t.Helper()
	facades := mock.Factory()
	facades.Log()
	// A company's tax id digest (theCompany) is keyed with APP_KEY.
	facades.Config().EXPECT().GetString("app.key").Return("test-app-key").Maybe()

	h := &bankHarness{
		blindpay:     mocks.NewMockProviderAdapter(t),
		limits:       mocks.NewMockPayoutLimitReader(t),
		bridge:       mocks.NewMockProviderAdapter(t),
		accounts:     mocks.NewMockBankAccountEnsurer(t),
		estimateRepo: mocks.NewMockRampEstimateRepository(t),
		mfa:          mocks.NewMockMfaStepUpValidator(t),
		flagOn:       true,
		registered:   map[string]uint{},
	}
	h.crypt = facades.Crypt()
	h.blindpay.EXPECT().Code().Return(models.ProviderBlindPay).Maybe()
	// BlindPay settles USDC and USDT only, as the real adapter declares.
	h.blindpay.EXPECT().Supports(testifymock.Anything).RunAndReturn(func(c ramp.Capability) bool {
		return c.Token != models.TargetTokenBRS
	}).Maybe()
	h.bridge.EXPECT().Code().Return(models.ProviderBridge).Maybe()
	h.bridge.EXPECT().Supports(testifymock.Anything).Return(true).Maybe()

	settings := mocks.NewMockSettingsSource(t)
	// Both bank rails are read: the options screen lists them together.
	for _, bankRail := range append([]ramp.Rail{rail}, ramp.BankRails()...) {
		group := ramp.RailGroup(bankRail)
		settings.EXPECT().String(group, ramp.SettingProviderOrder).
			Return(ramp.FormatProviderOrder([]string{models.ProviderBlindPay, models.ProviderBridge}), nil).Maybe()
		settings.EXPECT().String(group, ramp.FeeSettingKey(models.ProviderBlindPay)).Return("0", nil).Maybe()
		// A Pix payout the provider prepares is read in reais.
		settings.EXPECT().String(group, ramp.FeeSettingKey(models.ProviderBridge)).Return("1.50", nil).Maybe()
		settings.EXPECT().String(group, ramp.FeeSettingKey(models.ProviderOzav)).Return("1.50", nil).Maybe()
		settings.EXPECT().String(group, ramp.SettingMinAmountBRL).Return("", nil).Maybe()
		settings.EXPECT().String(group, ramp.SettingMaxAmountBRL).Return("", nil).Maybe()
		settings.EXPECT().String(group, ramp.SettingMinAmountBRLByToken).Return("", nil).Maybe()
		settings.EXPECT().String(group, ramp.SettingOzavOfframpFundingWindowMinutes).Return("30", nil).Maybe()
		settings.EXPECT().String(group, ramp.SettingOzavDepositMatchTolerance).Return("0", nil).Maybe()
		settings.EXPECT().String(group, ramp.SettingMinAmountUSD).
			RunAndReturn(func(string, string) (string, error) { return h.minUSD, nil }).Maybe()
		settings.EXPECT().String(group, ramp.SettingMaxAmountUSD).
			RunAndReturn(func(string, string) (string, error) { return h.maxUSD, nil }).Maybe()
	}

	integ := mocks.NewMockCustomerProviderIntegrationRepository(t)
	kycs := mocks.NewMockCustomerProviderKycRepository(t)
	for _, code := range []string{models.ProviderBlindPay, models.ProviderBridge} {
		providerGroup := ramp.ProviderGroup(code)
		settings.EXPECT().Bool(providerGroup, ramp.SettingEnabled).Return(true, nil).Maybe()
		settings.EXPECT().String(providerGroup, ramp.SettingMode).Return(models.ProviderModeSandbox, nil).Maybe()
		// Any customer: the person and the company of the money-rule cases
		// are both linked and approved for the bank rails.
		integ.EXPECT().FindByCustomerAndProvider(testifymock.Anything, code).Return(&models.CustomerProviderIntegration{
			Provider: code, Status: models.IntegrationStatusActive,
			ExternalCustomerID: support.Ptr(blindpayCusID), Metadata: "{}",
		}, nil).Maybe()
		approved := &models.CustomerProviderKyc{Provider: code, KycStatus: models.KycStatusApproved}
		require.NoError(t, approved.SetEndorsements([]models.Endorsement{
			{Name: models.EndorsementBankPayout, Status: models.EndorsementStatusApproved},
			{Name: models.EndorsementPix, Status: models.EndorsementStatusApproved},
		}))
		kycs.EXPECT().FindByCustomerAndProvider(testifymock.Anything, code).Return(approved, nil).Maybe()
	}

	factory := ramp.NewFactory(first(h), h.bridge)
	config := ramp.NewConfig(settings)
	router := ramp.NewRouter(ramp.RouterDeps{
		Factory: factory, Config: config, Integrations: integ, Kyc: kycs,
		Persons: ramp.Persons{Customers: support.IndividualCustomers(t), Profiles: noRetainedProfile(t)},
		AppEnv:  "local",
	})

	features := mocks.NewMockFeature(t)
	features.EXPECT().IsActive(testifymock.Anything, rail.Flag(), models.UserFlagScope(userID)).
		RunAndReturn(func(context.Context, string, any) (bool, error) { return h.flagOn, nil }).Maybe()
	// A Pix payout a provider prepared is gated by its own rail's switch.
	features.EXPECT().IsActive(testifymock.Anything, ramp.RailPixPayout.Flag(), models.UserFlagScope(userID)).
		RunAndReturn(func(context.Context, string, any) (bool, error) { return h.flagOn, nil }).Maybe()

	wallets := mocks.NewMockCustomerWalletRepository(t)
	wallets.EXPECT().FindByAddress(testifymock.Anything, testifymock.Anything).
		RunAndReturn(func(chain, address string) (*models.CustomerWallet, error) {
			return registeredWallet(h.registered, chain, address), nil
		}).Maybe()

	h.deps = ramps.Deps{
		Router: router, Config: config, Estimates: h.estimateRepo, Wallets: wallets,
		Locker: passthroughLocker{}, Broadcaster: mocks.NewMockBroadcaster(t), Members: support.IndividualMembers(t),
		Features: features, PendingSteps: mocks.NewMockPendingStepsSource(t), Factory: factory,
		BeneficiaryAccounts: h.accounts,
	}
	h.rebuild(t, h.deps)
	return h
}

// rebuild builds the off-ramp service from d: a test swaps one collaborator
// in a copy of h.deps, which no setter on a built service could express.
func (h *bankHarness) rebuild(t *testing.T, d ramps.Deps) {
	h.offramp = ramps.NewOfframpService(d, mocks.NewMockOfframpDestinationRepository(t), h.mfa, "local", nil)
}

func bankInput(rail ramp.Rail) dto.EstimateOfframpDTO {
	return dto.EstimateOfframpDTO{
		Rail: rail.String(), BankBeneficiaryID: beneficiaryID,
		AmountToken: "100", Token: "usdc", Chain: "solana", FromAddress: wallet,
	}
}

func registeredAccount() *ramp.BankPayoutAccount {
	return &ramp.BankPayoutAccount{ExternalAccountID: blindpayBankAccountID, BeneficiaryRevision: 3, CustomerExternalID: blindpayCusID}
}

func (h *bankHarness) ensures(rail ramp.Rail) { h.ensuresFor(userID, rail) }

func (h *bankHarness) ensuresFor(customerID uint, rail ramp.Rail) {
	h.accounts.EXPECT().Ensure(testifymock.Anything, customerID, beneficiaryID, models.ProviderBlindPay, rail).
		Return(registeredAccount(), nil).Once()
}

// quotes answers the bank quote with the amount that leaves the wallet
// (source) and the one that lands (receive).
func (h *bankHarness) quotes(source, receive string) {
	h.blindpay.EXPECT().EstimateOfframp(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(&ramp.Estimate{
			Binding: true, Rate: "0.998000", FeeAmount: "0.00", FeeCurrency: "usdc",
			SourceAmount: source, EstimatedReceive: receive, ProviderFeeAmount: "2.70",
		}, nil).Once()
}

func estimateBank(h *bankHarness, rail ramp.Rail) (*ramps.EstimateView, error) {
	return h.offramp.Estimate(context.Background(), user, person, bankInput(rail))
}

// The quote prices the registered account, persists the whole destination the
// create will compare, and says it pays out in dollars.
func TestEstimateBankStoresTheRegisteredDestination(t *testing.T) {
	for _, tc := range []struct {
		rail        ramp.Rail
		paymentRail string
	}{{ramp.RailACHPayout, "ach"}, {ramp.RailWirePayout, "wire"}} {
		t.Run(tc.rail.String(), func(t *testing.T) {
			h := newBankHarness(t, tc.rail, true)
			h.ensures(tc.rail)
			expires := time.Now().Add(5 * time.Minute)
			h.blindpay.EXPECT().EstimateOfframp(testifymock.Anything,
				ramp.CustomerRef{CustomerID: userID, ExternalID: blindpayCusID},
				ramp.OfframpQuoteRequest{
					Source: ramp.CryptoLeg{Currency: "usdc", PaymentRail: "solana", Amount: "100", Address: wallet},
					Bank:   &ramp.BankDestination{Rail: tc.rail, Currency: "usd", ExternalAccountID: blindpayBankAccountID},
					// The bank rail's fee, as the policy stores it.
					FeePercent: "0",
				}).
				Return(&ramp.Estimate{
					Binding: true, ExpiresAt: &expires, ProviderQuoteID: "qu_1",
					Rate: "0.998000", FeeAmount: "0.00", FeeCurrency: "usdc",
					SourceAmount: "100.00", EstimatedReceive: "97.30", ProviderFeeAmount: "2.70",
				}, nil).Once()
			h.limits.EXPECT().RemainingPayoutUSD(testifymock.Anything,
				ramp.CustomerRef{CustomerID: userID, ExternalID: blindpayCusID}).Return("1000.00", nil).Once()
			var stored *models.RampEstimate
			h.estimateRepo.EXPECT().Create(testifymock.Anything).Return(nil).Once().
				Run(func(args testifymock.Arguments) { stored = args.Get(0).(*models.RampEstimate) })

			view, err := estimateBank(h, tc.rail)

			require.NoError(t, err)
			require.Equal(t, models.ProviderBlindPay, stored.Provider)
			require.Equal(t, tc.rail.String(), stored.Rail)
			require.Equal(t, "0", stored.FeePercent)
			require.Equal(t, "qu_1", *stored.ProviderQuoteID)
			require.True(t, stored.Binding)

			var snapshot map[string]any
			require.NoError(t, json.Unmarshal([]byte(stored.RawResponse), &snapshot))
			require.Equal(t, map[string]any{
				"rail": tc.rail.String(), "source_amount": "100.00", "source_currency": "usdc",
				"destination_currency": "usd", "destination_payment_rail": tc.paymentRail,
				"chain": "solana", "from_address": wallet,
				"rate": "0.998000", "fee_percent": "0", "fee_amount": "0.00", "fee_currency": "usdc",
				"estimated_receive": "97.30", "provider_fee_amount": "2.70",
				"bank_beneficiary_id": beneficiaryID, "beneficiary_revision": float64(3),
				"bank_account_id": blindpayBankAccountID, "customer_external_id": blindpayCusID,
			}, snapshot)

			require.Equal(t, &ramps.EstimateView{
				EstimateID: stored.ExternalID, Rail: tc.rail.String(),
				Rate: "0.998000", FeePercent: "0", FeeAmount: "0.00", FeeCurrency: "usdc",
				SourceAmount: "100.00", EstimatedReceive: "97.30", ProviderFeeAmount: "2.70",
				Binding: true, ExpiresAt: &expires, DestinationCurrency: "usd",
			}, view)
		})
	}
}

// A closed rail refuses before any beneficiary is registered with a provider.
func TestEstimateBankOnAClosedRailRegistersNothing(t *testing.T) {
	h := newBankHarness(t, ramp.RailACHPayout, true)
	h.flagOn = false

	_, err := estimateBank(h, ramp.RailACHPayout)

	require.ErrorIs(t, err, ramp.ErrRailDisabled)
	h.accounts.AssertNotCalled(t, "Ensure", testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything)
	h.estimateRepo.AssertNotCalled(t, "Create", testifymock.Anything)
}

// A beneficiary that is not the customer's is the ramps service's own
// sentinel, so the controller maps it without importing the bank package.
func TestEstimateBankPassesAnUnknownBeneficiary(t *testing.T) {
	h := newBankHarness(t, ramp.RailACHPayout, true)
	h.accounts.EXPECT().Ensure(testifymock.Anything, userID, beneficiaryID, models.ProviderBlindPay, ramp.RailACHPayout).
		Return(nil, fmt.Errorf("bank: find beneficiary: %w", repositories.ErrBankBeneficiaryNotFound)).Once()

	_, err := estimateBank(h, ramp.RailACHPayout)

	require.ErrorIs(t, err, ramps.ErrBankBeneficiaryNotFound)
	h.blindpay.AssertNotCalled(t, "EstimateOfframp", testifymock.Anything, testifymock.Anything, testifymock.Anything)
	h.estimateRepo.AssertNotCalled(t, "Create", testifymock.Anything)
}

// The ramps sentinel is the bank service's value, not a look-alike: whichever
// of the two an error carries, errors.Is matches the other.
func TestErrBankBeneficiaryNotFoundIsTheBankServicesSentinel(t *testing.T) {
	require.ErrorIs(t, bank.ErrBankBeneficiaryNotFound, ramps.ErrBankBeneficiaryNotFound)
	require.ErrorIs(t, fmt.Errorf("ensure: %w", bank.ErrBankBeneficiaryNotFound), ramps.ErrBankBeneficiaryNotFound)
}

// An account the provider is still reviewing leaves as it came, unquoted: the
// envelope names it bank_account_verifying.
func TestEstimateBankPassesAnAccountStillVerifying(t *testing.T) {
	h := newBankHarness(t, ramp.RailACHPayout, true)
	h.accounts.EXPECT().Ensure(testifymock.Anything, userID, beneficiaryID, models.ProviderBlindPay, ramp.RailACHPayout).
		Return(nil, ramp.ErrBankAccountVerifying).Once()

	_, err := estimateBank(h, ramp.RailACHPayout)

	require.ErrorIs(t, err, ramp.ErrBankAccountVerifying)
	h.blindpay.AssertNotCalled(t, "EstimateOfframp", testifymock.Anything, testifymock.Anything, testifymock.Anything)
	h.estimateRepo.AssertNotCalled(t, "Create", testifymock.Anything)
}

// A provider that fails is not failed over: the account is registered with
// it, and the next provider has nothing to pay out to.
func TestEstimateBankDoesNotFailOver(t *testing.T) {
	h := newBankHarness(t, ramp.RailACHPayout, true)
	h.ensures(ramp.RailACHPayout)
	h.blindpay.EXPECT().EstimateOfframp(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(nil, ramp.ErrRetryableUpstream).Once()

	_, err := estimateBank(h, ramp.RailACHPayout)

	require.ErrorIs(t, err, ramp.ErrRetryableUpstream)
	// Strict mocks: a second Ensure (for Bridge) or a Bridge quote would fail
	// the test on its own; these say so out loud.
	h.accounts.AssertNumberOfCalls(t, "Ensure", 1)
	h.bridge.AssertNotCalled(t, "EstimateOfframp", testifymock.Anything, testifymock.Anything, testifymock.Anything)
	h.estimateRepo.AssertNotCalled(t, "Create", testifymock.Anything)
}

// The rail's floor is in USD of the source stablecoin, and the refusal names
// it in dollars.
func TestEstimateBankBelowTheMinimumNamesTheBoundInUSD(t *testing.T) {
	h := newBankHarness(t, ramp.RailACHPayout, true)
	h.minUSD = "10.00"
	h.ensures(ramp.RailACHPayout)
	h.quotes("9.50", "9.00")
	h.limits.EXPECT().RemainingPayoutUSD(testifymock.Anything, testifymock.Anything).Return("1000.00", nil).Once()

	_, err := estimateBank(h, ramp.RailACHPayout)

	var violation *ramp.LimitViolation
	require.ErrorAs(t, err, &violation)
	require.ErrorIs(t, err, ramp.ErrAmountBelowMinimum)
	require.Equal(t, "10.00", violation.LimitUSD)
	require.Empty(t, violation.LimitBRL)
	h.estimateRepo.AssertNotCalled(t, "Create", testifymock.Anything)
}

// The rail's ceiling is in dollars too.
func TestEstimateBankAboveTheMaximumNamesTheBoundInUSD(t *testing.T) {
	h := newBankHarness(t, ramp.RailACHPayout, true)
	h.maxUSD = "50.00"
	h.ensures(ramp.RailACHPayout)
	h.quotes("100.00", "97.30")
	h.limits.EXPECT().RemainingPayoutUSD(testifymock.Anything, testifymock.Anything).Return("1000.00", nil).Once()

	_, err := estimateBank(h, ramp.RailACHPayout)

	var violation *ramp.LimitViolation
	require.ErrorAs(t, err, &violation)
	require.ErrorIs(t, err, ramp.ErrAmountAboveMaximum)
	require.Equal(t, "50.00", violation.LimitUSD)
}

// What the provider still lets the customer pay out caps what leaves the
// wallet, not what lands: a source over the allowance is refused even though
// its receive fits, and the refusal names the allowance as the limit.
func TestEstimateBankAboveTheRemainingAllowanceNamesIt(t *testing.T) {
	h := newBankHarness(t, ramp.RailACHPayout, true)
	h.ensures(ramp.RailACHPayout)
	h.quotes("50.50", "49.80")
	h.limits.EXPECT().RemainingPayoutUSD(testifymock.Anything, testifymock.Anything).Return("50.00", nil).Once()

	_, err := estimateBank(h, ramp.RailACHPayout)

	var violation *ramp.LimitViolation
	require.ErrorAs(t, err, &violation)
	require.ErrorIs(t, err, ramp.ErrAmountAboveMaximum)
	require.Equal(t, "50.00", violation.LimitUSD)
	h.estimateRepo.AssertNotCalled(t, "Create", testifymock.Anything)
}

// The reverse: a source within the allowance passes, whatever the receive.
func TestEstimateBankWithinTheRemainingAllowanceBySourcePasses(t *testing.T) {
	h := newBankHarness(t, ramp.RailACHPayout, true)
	h.ensures(ramp.RailACHPayout)
	h.quotes("49.90", "50.10")
	h.limits.EXPECT().RemainingPayoutUSD(testifymock.Anything, testifymock.Anything).Return("50.00", nil).Once()
	h.estimateRepo.EXPECT().Create(testifymock.Anything).Return(nil).Once()

	view, err := estimateBank(h, ramp.RailACHPayout)

	require.NoError(t, err)
	require.Equal(t, "49.90", view.SourceAmount)
}

// A quote that names no source amount is bounded by the one typed.
func TestEstimateBankFallsBackToTheTypedAmountForLimits(t *testing.T) {
	h := newBankHarness(t, ramp.RailACHPayout, true)
	h.ensures(ramp.RailACHPayout)
	h.quotes("", "49.00")
	h.limits.EXPECT().RemainingPayoutUSD(testifymock.Anything, testifymock.Anything).Return("50.00", nil).Once()

	_, err := estimateBank(h, ramp.RailACHPayout) // typed: "100"

	var violation *ramp.LimitViolation
	require.ErrorAs(t, err, &violation)
	require.Equal(t, "50.00", violation.LimitUSD)
}

// A business's bank payout owes its money rules before any account is
// registered: an address it did not register is refused, as on Pix.
func TestEstimateBankForABusinessFromAnUnregisteredWalletIsRefused(t *testing.T) {
	h := newBankHarness(t, ramp.RailACHPayout, true)

	_, err := h.offramp.Estimate(context.Background(), user, theCompany(models.CustomerStatusActive), bankInput(ramp.RailACHPayout))

	require.ErrorIs(t, err, ramps.ErrWalletNotRegistered)
	h.accounts.AssertNotCalled(t, "Ensure", testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything)
	h.estimateRepo.AssertNotCalled(t, "Create", testifymock.Anything)
}

// A company that is not active moves no money, to a bank either.
func TestEstimateBankForAnInactiveBusinessIsRefused(t *testing.T) {
	h := newBankHarness(t, ramp.RailACHPayout, true)
	h.registered[wallet] = businessID

	_, err := h.offramp.Estimate(context.Background(), user, theCompany(models.CustomerStatusDraft), bankInput(ramp.RailACHPayout))

	require.ErrorIs(t, err, ramp.ErrKycPending)
	h.accounts.AssertNotCalled(t, "Ensure", testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// An active business paying from its own registered wallet is quoted, with
// no Pix key rule in the way.
func TestEstimateBankForABusinessFromItsOwnWalletIsQuoted(t *testing.T) {
	h := newBankHarness(t, ramp.RailACHPayout, true)
	h.registered[wallet] = businessID
	h.ensuresFor(businessID, ramp.RailACHPayout)
	h.quotes("100.00", "97.30")
	h.limits.EXPECT().RemainingPayoutUSD(testifymock.Anything, testifymock.Anything).Return("1000.00", nil).Once()
	h.estimateRepo.EXPECT().Create(testifymock.Anything).Return(nil).Once()

	_, err := h.offramp.Estimate(context.Background(), user, theCompany(models.CustomerStatusActive), bankInput(ramp.RailACHPayout))

	require.NoError(t, err)
}

// A provider that cannot read the allowance caps nothing.
func TestEstimateBankWithoutALimitReaderHasNoProviderCap(t *testing.T) {
	h := newBankHarness(t, ramp.RailACHPayout, false)
	h.ensures(ramp.RailACHPayout)
	h.quotes("100.00", "97.30")
	h.estimateRepo.EXPECT().Create(testifymock.Anything).Return(nil).Once()

	view, err := estimateBank(h, ramp.RailACHPayout)

	require.NoError(t, err)
	require.Equal(t, "97.30", view.EstimatedReceive)
	h.limits.AssertNotCalled(t, "RemainingPayoutUSD", testifymock.Anything, testifymock.Anything)
}

// An allowance the provider could not read refuses the quote rather than
// quote without the cap.
func TestEstimateBankPassesAFailedAllowanceRead(t *testing.T) {
	h := newBankHarness(t, ramp.RailACHPayout, true)
	h.ensures(ramp.RailACHPayout)
	h.quotes("100.00", "97.30")
	h.limits.EXPECT().RemainingPayoutUSD(testifymock.Anything, testifymock.Anything).
		Return("", ramp.ErrRetryableUpstream).Once()

	_, err := estimateBank(h, ramp.RailACHPayout)

	require.ErrorIs(t, err, ramp.ErrRetryableUpstream)
	h.estimateRepo.AssertNotCalled(t, "Create", testifymock.Anything)
}

// A Pix estimate — no rail, or pix_payout — never reaches the bank path: no
// account is ensured, and the quote is the Pix one it always was, in reais.
func TestEstimateOnPixNeverEnsuresABankAccount(t *testing.T) {
	for _, rail := range []string{"", models.RailPixPayout} {
		t.Run("rail="+rail, func(t *testing.T) {
			h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
			accounts := mocks.NewMockBankAccountEnsurer(t)
			h.rebuild(func(d *ramps.Deps) { d.BeneficiaryAccounts = accounts })
			h.adapter.EXPECT().EstimateOfframp(testifymock.Anything, testifymock.Anything, testifymock.Anything).
				Return(bridgeEstimate(), nil).Once()
			h.estimateRepo.EXPECT().Create(testifymock.Anything).Return(nil).Once()
			in := offrampInput()
			in.Rail = rail

			view, err := h.offramp.Estimate(context.Background(), user, person, in)

			require.NoError(t, err)
			require.Equal(t, models.RailPixPayout, view.Rail)
			require.Equal(t, "brl", view.DestinationCurrency)
			accounts.AssertNotCalled(t, "Ensure", testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything)
		})
	}
}

// A service built without the bank-account port refuses the bank path as a
// rail nobody can serve, instead of a nil dereference inside the resolve.
func TestEstimateBankWithoutTheAccountPortIsRefused(t *testing.T) {
	h := newBankHarness(t, ramp.RailACHPayout, true)
	d := h.deps
	d.BeneficiaryAccounts = nil
	h.rebuild(t, d)

	_, err := estimateBank(h, ramp.RailACHPayout)

	require.ErrorIs(t, err, ramp.ErrNoProviderAvailable)
	require.ErrorContains(t, err, "bank accounts not wired")
	h.blindpay.AssertNotCalled(t, "EstimateOfframp", testifymock.Anything, testifymock.Anything, testifymock.Anything)
	h.estimateRepo.AssertNotCalled(t, "Create", testifymock.Anything)
}
