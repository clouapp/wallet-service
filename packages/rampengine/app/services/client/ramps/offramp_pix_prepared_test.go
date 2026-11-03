package ramps_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/goravel/framework/testing/mock"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
	"github.com/macrowallets/waas/packages/rampengine/tests/support"
)

const pixBankID = "ba_pix0000001"

// pixPreparingAdapter is BlindPay as the service sees it for a Pix payout: a
// provider that registers the destination, prepares the funding and knows the
// customer's remaining allowance.
type pixPreparingAdapter struct {
	*mocks.MockProviderAdapter
	*mocks.MockOfframpPrepareAdapter
	*mocks.MockPixAccountRegistrar
	*mocks.MockPayoutLimitReader
}

// pixPreparedHarness serves pix_payout from BlindPay (which prepares funding)
// and Bridge (which takes a deposit), in the order a test sets.
type pixPreparedHarness struct {
	blindpay *mocks.MockProviderAdapter
	limits   *mocks.MockPayoutLimitReader
	bridge   *mocks.MockProviderAdapter
	accounts *mocks.MockPixAccountEnsurer
	estimate *mocks.MockRampEstimateRepository
	offramp  *ramps.OfframpService
	stored   *models.RampEstimate

	registrar      *mocks.MockPixAccountRegistrar
	order          []string
	minBRL, maxBRL string
	deposit        string
}

func newPixPreparedHarness(t *testing.T, order ...string) *pixPreparedHarness {
	t.Helper()
	facades := mock.Factory()
	facades.Log()
	facades.Config().EXPECT().GetString("app.key").Return("test-app-key").Maybe()

	h := &pixPreparedHarness{
		blindpay: mocks.NewMockProviderAdapter(t), limits: mocks.NewMockPayoutLimitReader(t),
		bridge: mocks.NewMockProviderAdapter(t), accounts: mocks.NewMockPixAccountEnsurer(t),
		estimate: mocks.NewMockRampEstimateRepository(t),
		order:    order,
	}
	if len(order) == 0 {
		h.order = []string{models.ProviderBlindPay, models.ProviderBridge}
	}
	h.blindpay.EXPECT().Code().Return(models.ProviderBlindPay).Maybe()
	h.blindpay.EXPECT().Supports(testifymock.Anything).Return(true).Maybe()
	h.bridge.EXPECT().Code().Return(models.ProviderBridge).Maybe()
	h.bridge.EXPECT().Supports(testifymock.Anything).Return(true).Maybe()

	settings := mocks.NewMockSettingsSource(t)
	group := ramp.RailGroup(ramp.RailPixPayout)
	settings.EXPECT().String(group, ramp.SettingProviderOrder).
		RunAndReturn(func(string, string) (string, error) { return ramp.FormatProviderOrder(h.order), nil }).Maybe()
	for _, code := range ramp.FeeProviderCodes() {
		fee := "1.50"
		if code == models.ProviderBlindPay {
			fee = "0.50"
		}
		settings.EXPECT().String(group, ramp.FeeSettingKey(code)).Return(fee, nil).Maybe()
	}
	settings.EXPECT().String(group, ramp.SettingMinAmountBRL).
		RunAndReturn(func(string, string) (string, error) { return h.minBRL, nil }).Maybe()
	settings.EXPECT().String(group, ramp.SettingMaxAmountBRL).
		RunAndReturn(func(string, string) (string, error) { return h.maxBRL, nil }).Maybe()
	settings.EXPECT().String(group, ramp.SettingMinAmountBRLByToken).Return("", nil).Maybe()
	settings.EXPECT().String(group, ramp.SettingOzavOfframpFundingWindowMinutes).Return("30", nil).Maybe()
	settings.EXPECT().String(group, ramp.SettingOzavDepositMatchTolerance).Return("0", nil).Maybe()

	integ := mocks.NewMockCustomerProviderIntegrationRepository(t)
	kycs := mocks.NewMockCustomerProviderKycRepository(t)
	for _, code := range []string{models.ProviderBlindPay, models.ProviderBridge} {
		providerGroup := ramp.ProviderGroup(code)
		settings.EXPECT().Bool(providerGroup, ramp.SettingEnabled).Return(true, nil).Maybe()
		settings.EXPECT().String(providerGroup, ramp.SettingMode).Return(models.ProviderModeSandbox, nil).Maybe()
		integ.EXPECT().FindByCustomerAndProvider(testifymock.Anything, code).Return(&models.CustomerProviderIntegration{
			Provider: code, Status: models.IntegrationStatusActive,
			ExternalCustomerID: support.Ptr(blindpayCusID), Metadata: "{}",
		}, nil).Maybe()
		approved := &models.CustomerProviderKyc{Provider: code, KycStatus: models.KycStatusApproved}
		require.NoError(t, approved.SetEndorsements([]models.Endorsement{
			{Name: models.EndorsementPix, Status: models.EndorsementStatusApproved},
			{Name: models.EndorsementBankPayout, Status: models.EndorsementStatusApproved},
		}))
		kycs.EXPECT().FindByCustomerAndProvider(testifymock.Anything, code).Return(approved, nil).Maybe()
	}

	first := &pixPreparingAdapter{
		MockProviderAdapter: h.blindpay, MockOfframpPrepareAdapter: mocks.NewMockOfframpPrepareAdapter(t),
		MockPixAccountRegistrar: mocks.NewMockPixAccountRegistrar(t), MockPayoutLimitReader: h.limits,
	}
	factory := ramp.NewFactory(first, h.bridge)
	config := ramp.NewConfig(settings)
	router := ramp.NewRouter(ramp.RouterDeps{
		Factory: factory, Config: config, Integrations: integ, Kyc: kycs,
		Persons: ramp.Persons{Customers: support.IndividualCustomers(t), Profiles: noRetainedProfile(t)},
		AppEnv:  "local",
	})
	txRepo := mocks.NewMockTransactionRepository(t)
	txRepo.EXPECT().FindLatestOfframpDepositAddress(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		RunAndReturn(func(uint, string, string) (string, error) { return h.deposit, nil }).Maybe()

	wallets := mocks.NewMockCustomerWalletRepository(t)
	wallets.EXPECT().FindByAddress(testifymock.Anything, testifymock.Anything).Return(nil, nil).Maybe()
	deps := ramps.Deps{
		Router: router, Config: config, Transactions: txRepo, Estimates: h.estimate,
		Locker: passthroughLocker{}, Broadcaster: mocks.NewMockBroadcaster(t), Members: support.IndividualMembers(t),
		Features: openFeatures(), PendingSteps: mocks.NewMockPendingStepsSource(t), Factory: factory,
		Wallets: wallets, PixAccounts: h.accounts,
	}
	h.offramp = ramps.NewOfframpService(deps, mocks.NewMockOfframpDestinationRepository(t), mocks.NewMockMfaStepUpValidator(t), "local", nil)
	h.estimate.EXPECT().Create(testifymock.Anything).Return(nil).Maybe().
		Run(func(args testifymock.Arguments) { h.stored = args.Get(0).(*models.RampEstimate) })

	// The adapters' optional ports are mocks of their own.
	h.registrar = first.MockPixAccountRegistrar
	return h
}

func pixPreparedInput(formats ...string) dto.EstimateOfframpDTO {
	in := offrampInput()
	in.FundingFormats = formats
	return in
}

func (h *pixPreparedHarness) snapshot(t *testing.T) map[string]any {
	t.Helper()
	require.NotNil(t, h.stored, "no estimate was stored")
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(h.stored.RawResponse), &out))
	return out
}

func (h *pixPreparedHarness) blindpayQuotes(receive string) {
	h.blindpay.EXPECT().EstimateOfframp(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(&ramp.Estimate{
			Binding: true, ProviderQuoteID: "qu_1", Rate: "5.40", FeeAmount: "0.50", FeeCurrency: "usdc",
			SourceAmount: "100.00", EstimatedReceive: receive,
		}, nil).Once()
}

func (h *pixPreparedHarness) bridgeQuotes() {
	h.bridge.EXPECT().EstimateOfframp(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(&ramp.Estimate{
			Binding: true, Rate: "5.30", FeeAmount: "1.50", FeeCurrency: "usdc",
			SourceAmount: "100.00", EstimatedReceive: "530.00",
		}, nil).Once()
}

func (h *pixPreparedHarness) ensures() {
	h.accounts.EXPECT().Ensure(testifymock.Anything, testifymock.Anything, testifymock.Anything, person, testifymock.Anything).
		Return(pixBankID, nil).Once()
}

func (h *pixPreparedHarness) estimateAs(in dto.EstimateOfframpDTO) (*ramps.EstimateView, error) {
	return h.offramp.Estimate(context.Background(), user, person, in)
}

func TestEstimatePixWithAPreparingProviderRegistersTheAccountAndQuotesIt(t *testing.T) {
	h := newPixPreparedHarness(t)
	h.ensures()
	h.blindpayQuotes("540.00")
	h.limits.EXPECT().RemainingPayoutUSD(testifymock.Anything, testifymock.Anything).Return("1000.00", nil).Once()

	view, err := h.estimateAs(pixPreparedInput("deposit", "prepared"))

	require.NoError(t, err)
	require.Equal(t, ramps.DepositModePrepared, view.DepositMode)
	require.Equal(t, "brl", view.DestinationCurrency)
	require.Empty(t, view.DepositToAddress)
	snapshot := h.snapshot(t)
	require.Equal(t, models.FundingModePrepared, snapshot["funding_mode"])
	require.Equal(t, pixBankID, snapshot["bank_account_id"])
	require.Equal(t, blindpayCusID, snapshot["customer_external_id"])
	require.Equal(t, "540.00", snapshot["estimated_receive"])
	require.Equal(t, models.ProviderBlindPay, h.stored.Provider)
	require.Equal(t, "0.50", h.stored.FeePercent)
}

func TestEstimatePixStillWorksForBridgeAndOzavExactlyAsBefore(t *testing.T) {
	h := newPixPreparedHarness(t, models.ProviderBridge)
	h.bridgeQuotes()

	view, err := h.estimateAs(pixPreparedInput("deposit", "prepared"))

	require.NoError(t, err)
	require.Equal(t, ramps.DepositModeSimulated, view.DepositMode)
	require.NotContains(t, h.snapshot(t), "funding_mode")
	require.NotContains(t, h.snapshot(t), "bank_account_id")
	h.accounts.AssertNotCalled(t, "Ensure", testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

func TestEstimatePixFailoverToBlindPayNeverMixesTheFormats(t *testing.T) {
	t.Run("Bridge fails, BlindPay answers: the estimate is prepared", func(t *testing.T) {
		h := newPixPreparedHarness(t, models.ProviderBridge, models.ProviderBlindPay)
		h.bridge.EXPECT().EstimateOfframp(testifymock.Anything, testifymock.Anything, testifymock.Anything).
			Return(nil, ramp.ErrProviderFailed).Once()
		h.ensures()
		h.blindpayQuotes("540.00")
		h.limits.EXPECT().RemainingPayoutUSD(testifymock.Anything, testifymock.Anything).Return("1000.00", nil).Once()

		view, err := h.estimateAs(pixPreparedInput("prepared"))

		require.NoError(t, err)
		require.Equal(t, ramps.DepositModePrepared, view.DepositMode)
		require.Equal(t, models.FundingModePrepared, h.snapshot(t)["funding_mode"])
		require.Equal(t, models.ProviderBlindPay, h.stored.Provider)
	})
	t.Run("BlindPay fails, Bridge answers: a deposit estimate with nothing of the account", func(t *testing.T) {
		h := newPixPreparedHarness(t)
		h.ensures()
		h.blindpay.EXPECT().EstimateOfframp(testifymock.Anything, testifymock.Anything, testifymock.Anything).
			Return(nil, ramp.ErrProviderFailed).Once()
		h.bridgeQuotes()

		view, err := h.estimateAs(pixPreparedInput("prepared"))

		require.NoError(t, err)
		require.Equal(t, ramps.DepositModeSimulated, view.DepositMode)
		snapshot := h.snapshot(t)
		require.NotContains(t, snapshot, "funding_mode")
		require.NotContains(t, snapshot, "bank_account_id")
		require.NotContains(t, snapshot, "customer_external_id")
		require.Equal(t, models.ProviderBridge, h.stored.Provider)
	})
}

func TestEstimatePixBelowTheProviderMinimumNamesLimitUSDAndRegistersNothingTwice(t *testing.T) {
	h := newPixPreparedHarness(t, models.ProviderBlindPay)
	h.ensures()
	h.blindpay.EXPECT().EstimateOfframp(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(nil, &ramp.LimitViolation{LimitUSD: "10.00", Err: ramp.ErrAmountBelowMinimum}).Once()

	_, err := h.estimateAs(pixPreparedInput("prepared"))

	var violation *ramp.LimitViolation
	require.ErrorAs(t, err, &violation)
	require.Equal(t, "10.00", violation.LimitUSD)
	require.ErrorIs(t, err, ramp.ErrAmountBelowMinimum)
	require.Nil(t, h.stored)
}

func TestEstimatePixBelowTheRailMinimumInBRLNamesLimitBRL(t *testing.T) {
	h := newPixPreparedHarness(t)
	h.minBRL = "600.00"
	h.ensures()
	h.blindpayQuotes("540.00")
	h.limits.EXPECT().RemainingPayoutUSD(testifymock.Anything, testifymock.Anything).Return("1000.00", nil).Maybe()

	_, err := h.estimateAs(pixPreparedInput("prepared"))

	var violation *ramp.LimitViolation
	require.ErrorAs(t, err, &violation)
	require.Equal(t, "600.00", violation.LimitBRL)
	require.ErrorIs(t, err, ramp.ErrAmountBelowMinimum)
	require.Nil(t, h.stored)
}

func TestEstimatePixAboveTheCustomersRemainingAllowanceIsRefused(t *testing.T) {
	h := newPixPreparedHarness(t)
	h.ensures()
	h.blindpayQuotes("540.00")
	h.limits.EXPECT().RemainingPayoutUSD(testifymock.Anything, testifymock.Anything).Return("50.00", nil).Once()

	_, err := h.estimateAs(pixPreparedInput("prepared"))

	var violation *ramp.LimitViolation
	require.ErrorAs(t, err, &violation)
	require.Equal(t, "50.00", violation.LimitUSD)
	require.ErrorIs(t, err, ramp.ErrAmountAboveMaximum)
	require.Nil(t, h.stored)
}

func TestEstimatePixAccountVerifyingAnswersBankAccountVerifying(t *testing.T) {
	h := newPixPreparedHarness(t)
	h.accounts.EXPECT().Ensure(testifymock.Anything, testifymock.Anything, testifymock.Anything, person, testifymock.Anything).
		Return("", ramp.ErrBankAccountVerifying).Once()

	_, err := h.estimateAs(pixPreparedInput("prepared"))

	require.ErrorIs(t, err, ramp.ErrBankAccountVerifying)
	h.blindpay.AssertNotCalled(t, "EstimateOfframp", testifymock.Anything, testifymock.Anything, testifymock.Anything)
	require.Nil(t, h.stored)
}

func TestEstimatePixWithoutFundingFormatsNeverQuotesWithAPreparingProvider(t *testing.T) {
	h := newPixPreparedHarness(t)
	h.bridgeQuotes()

	view, err := h.estimateAs(pixPreparedInput())

	require.NoError(t, err)
	require.Equal(t, ramps.DepositModeSimulated, view.DepositMode)
	require.Equal(t, models.ProviderBridge, h.stored.Provider)
	h.accounts.AssertNotCalled(t, "Ensure", testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything)
	h.blindpay.AssertNotCalled(t, "EstimateOfframp", testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

func TestEstimatePixWithOnlyBlindPayAndNoDeclarationIsNoProviderAvailable(t *testing.T) {
	h := newPixPreparedHarness(t, models.ProviderBlindPay)

	_, err := h.estimateAs(pixPreparedInput("deposit"))

	require.ErrorIs(t, err, ramp.ErrNoProviderAvailable)
	require.Nil(t, h.stored)
}

func TestEstimatePixDeclaringPreparedButServedByBridgeGetsADepositEstimate(t *testing.T) {
	h := newPixPreparedHarness(t, models.ProviderBridge, models.ProviderBlindPay)
	h.bridgeQuotes()

	view, err := h.estimateAs(pixPreparedInput("prepared"))

	require.NoError(t, err)
	require.Equal(t, ramps.DepositModeSimulated, view.DepositMode)
	require.Equal(t, models.ProviderBridge, h.stored.Provider)
}

func TestEstimatePixIgnoresAnUnknownFundingFormatAndTooManyItems(t *testing.T) {
	h := newPixPreparedHarness(t)
	h.bridgeQuotes()
	formats := []string{"wire", "", "PREPARED", "carrier-pigeon"}
	for range 40 {
		formats = append(formats, "noise")
	}

	view, err := h.estimateAs(pixPreparedInput(formats...))

	require.NoError(t, err)
	require.Equal(t, ramps.DepositModeSimulated, view.DepositMode)
	require.Equal(t, models.ProviderBridge, h.stored.Provider)
}

func TestEstimatePixOfACompanyRunsTheMoneyRulesBeforeAnyAccountIsRegistered(t *testing.T) {
	h := newPixPreparedHarness(t)
	company := theCompany(models.CustomerStatusActive)

	// A company pays out only to its own CNPJ key: a CPF key is refused first.
	in := pixPreparedInput("prepared")
	_, err := h.offramp.Estimate(context.Background(), user, company, in)

	require.Error(t, err)
	h.accounts.AssertNotCalled(t, "Ensure", testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything)
	h.blindpay.AssertNotCalled(t, "EstimateOfframp", testifymock.Anything, testifymock.Anything, testifymock.Anything)
	require.Nil(t, h.stored)
}
