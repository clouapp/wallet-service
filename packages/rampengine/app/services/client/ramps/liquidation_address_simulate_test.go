package ramps_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
)

func simulateInput() dto.SimulatePayoutDepositDTO {
	return dto.SimulatePayoutDepositDTO{
		Rail: models.RailUSDACHPayout, BankAccountID: bankAccountID, Token: "USDC", Chain: "solana", Amount: "25.00",
	}
}

// The sandbox opens a drain and sends no webhook to a local server; the
// drain is read back and recorded the way the webhook would record it.
func TestSimulatePayoutDepositRecordsTheDrainTheSandboxOpened(t *testing.T) {
	h := newPayoutAddressHarness(t)
	transactions := mocks.NewMockTransactionRepository(t)
	broadcaster := mocks.NewMockBroadcaster(t)
	h.deps.Transactions, h.deps.Broadcaster = transactions, broadcaster
	h.rebuild()

	h.banks.EXPECT().FindOwned(userID, bankAccountID).Return(usBankAccount(), nil).Once()
	h.addresses.EXPECT().FindActiveRoute(userID, bankAccountID, models.RailUSDACHPayout, "solana", "usdc").
		Return(activeAddress(models.RailUSDACHPayout), nil).Once()
	h.payout.MockLiquidationAddressAdapter.EXPECT().SimulateLiquidationDeposit(testifymock.Anything, bridgeRef, "la_1",
		testifymock.MatchedBy(func(req ramp.SimulatedDeposit) bool {
			return req.Currency == "usdc" && req.Amount == "25.00" && strings.HasPrefix(req.IdempotencyKey, "la-sim:")
		})).Return("drn_sim_1", nil).Once()
	h.payout.MockLiquidationAddressAdapter.EXPECT().GetDrain(testifymock.Anything, bridgeRef, "la_1", "drn_sim_1").
		Return(&ramp.LiquidationDrain{
			DrainID: "drn_sim_1", ProviderAddressID: "la_1", Status: models.TxStatusFundsReceived,
			SourceAmount: "25.00", PaymentRail: "ach",
		}, nil).Once()
	var created *models.Transaction
	transactions.EXPECT().Create(testifymock.Anything).RunAndReturn(func(tx *models.Transaction) error {
		created = tx
		return nil
	}).Once()
	broadcaster.EXPECT().Publish(userID, testifymock.Anything).Once()

	view, err := h.service.SimulateDeposit(context.Background(), payoutUser(), payoutCustomer(), simulateInput())
	require.NoError(t, err)
	require.Equal(t, "drn_sim_1", created.GatewayOrderID)
	require.Equal(t, models.FiatCurrencyUSD, created.FiatCurrency)
	require.Equal(t, uint(5), *created.LiquidationAddressID)
	require.Equal(t, created.ExternalID, view.ID)
	require.Equal(t, models.TransactionTypeOfframp, view.Type)
	require.Equal(t, models.RailUSDACHPayout, view.Rail)
	require.Equal(t, models.TxStatusFundsReceived, view.Status)
	require.Equal(t, "25.00", view.AmountToken)
	require.Equal(t, ramps.DepositModeSimulated, view.DepositMode)
}

// The sandbox refusing the amount is the user's answer, not a 500, and no
// order is recorded: the mocks are strict, so a drain read or a transaction
// create fails the test.
func TestSimulatePayoutDepositPassesTheProviderRefusingTheAmountThrough(t *testing.T) {
	h := newPayoutAddressHarness(t)
	h.deps.Transactions, h.deps.Broadcaster = mocks.NewMockTransactionRepository(t), mocks.NewMockBroadcaster(t)
	h.rebuild()

	h.banks.EXPECT().FindOwned(userID, bankAccountID).Return(usBankAccount(), nil).Once()
	h.addresses.EXPECT().FindActiveRoute(userID, bankAccountID, models.RailUSDACHPayout, "solana", "usdc").
		Return(activeAddress(models.RailUSDACHPayout), nil).Once()
	h.payout.MockLiquidationAddressAdapter.EXPECT().SimulateLiquidationDeposit(testifymock.Anything, bridgeRef, "la_1",
		testifymock.Anything).Return("", fmt.Errorf("bridge: simulated deposit refused: %w", ramp.ErrAmountBelowMinimum)).Once()

	_, err := h.service.SimulateDeposit(context.Background(), payoutUser(), payoutCustomer(), simulateInput())
	require.ErrorIs(t, err, ramp.ErrAmountBelowMinimum)
}

// A simulated deposit is the sandbox's send, a birth: a rail an operator
// closed by emptying its provider order refuses it as it refuses a new
// address, not only a rail switched off by its flag.
func TestSimulatePayoutDepositRefusesARailWithNoProvider(t *testing.T) {
	h := newPayoutAddressHarness(t)
	h.settings.ExpectedCalls = nil
	group := ramp.RailGroup(ramp.RailUSDACHPayout)
	h.settings.EXPECT().String(group, ramp.SettingProviderOrder).Return("", nil).Maybe()
	h.settings.EXPECT().String(group, testifymock.Anything).Return("", nil).Maybe()
	bridgeGroup := ramp.ProviderGroup(models.ProviderBridge)
	h.settings.EXPECT().Bool(bridgeGroup, ramp.SettingEnabled).Return(true, nil).Maybe()
	h.settings.EXPECT().String(bridgeGroup, ramp.SettingMode).Return(models.ProviderModeSandbox, nil).Maybe()
	h.banks.EXPECT().FindOwned(userID, bankAccountID).Return(usBankAccount(), nil).Once()
	h.addresses.EXPECT().FindActiveRoute(userID, bankAccountID, models.RailUSDACHPayout, "solana", "usdc").
		Return(activeAddress(models.RailUSDACHPayout), nil).Once()

	_, err := h.service.SimulateDeposit(context.Background(), payoutUser(), payoutCustomer(), simulateInput())
	require.ErrorIs(t, err, ramp.ErrNoProviderAvailable)
}

func TestSimulatePayoutDepositIsSandboxOnly(t *testing.T) {
	h := newPayoutAddressHarness(t)
	h.settings.ExpectedCalls = nil
	bridgeGroup := ramp.ProviderGroup(models.ProviderBridge)
	h.settings.EXPECT().Bool(bridgeGroup, ramp.SettingEnabled).Return(true, nil).Maybe()
	h.settings.EXPECT().String(bridgeGroup, ramp.SettingMode).Return(models.ProviderModeProduction, nil).Maybe()
	h.banks.EXPECT().FindOwned(userID, bankAccountID).Return(usBankAccount(), nil).Once()
	h.addresses.EXPECT().FindActiveRoute(userID, bankAccountID, models.RailUSDACHPayout, "solana", "usdc").
		Return(activeAddress(models.RailUSDACHPayout), nil).Once()

	_, err := h.service.SimulateDeposit(context.Background(), payoutUser(), payoutCustomer(), simulateInput())
	require.ErrorIs(t, err, ramp.ErrSandboxOnly)
}

func TestSimulatePayoutDepositNeedsTheAddress(t *testing.T) {
	h := newPayoutAddressHarness(t)
	h.banks.EXPECT().FindOwned(userID, bankAccountID).Return(usBankAccount(), nil).Once()
	h.addresses.EXPECT().FindActiveRoute(userID, bankAccountID, models.RailUSDACHPayout, "solana", "usdc").
		Return(nil, nil).Once()

	_, err := h.service.SimulateDeposit(context.Background(), payoutUser(), payoutCustomer(), simulateInput())
	require.ErrorIs(t, err, ramps.ErrOrderNotFound)
}

// The route is the customer's, the account the user's: an account the user
// does not hold is not found, whoever's route it would be.
func TestSimulatePayoutDepositRefusesABankAccountTheUserDoesNotHold(t *testing.T) {
	h := newPayoutAddressHarness(t)
	h.banks.EXPECT().FindOwned(userID, bankAccountID).Return(nil, nil).Once()

	_, err := h.service.SimulateDeposit(context.Background(), payoutUser(), payoutCustomer(), simulateInput())
	require.ErrorIs(t, err, ramps.ErrBankAccountNotFound)
}

// The sandbox's webhook recorded the drain before the simulation did: the
// insert loses uniq_tx_provider_deposit, and the order recorded is the answer.
func TestSimulatePayoutDepositReadsBackADrainTheWebhookRecorded(t *testing.T) {
	h := newPayoutAddressHarness(t)
	transactions := mocks.NewMockTransactionRepository(t)
	h.deps.Transactions = transactions
	h.rebuild()

	h.banks.EXPECT().FindOwned(userID, bankAccountID).Return(usBankAccount(), nil).Once()
	h.addresses.EXPECT().FindActiveRoute(userID, bankAccountID, models.RailUSDACHPayout, "solana", "usdc").
		Return(activeAddress(models.RailUSDACHPayout), nil).Once()
	h.payout.MockLiquidationAddressAdapter.EXPECT().SimulateLiquidationDeposit(testifymock.Anything, bridgeRef, "la_1",
		testifymock.Anything).Return("drn_sim_1", nil).Once()
	h.payout.MockLiquidationAddressAdapter.EXPECT().GetDrain(testifymock.Anything, bridgeRef, "la_1", "drn_sim_1").
		Return(&ramp.LiquidationDrain{DrainID: "drn_sim_1", ProviderAddressID: "la_1", Status: models.TxStatusFundsReceived}, nil).Once()
	transactions.EXPECT().Create(testifymock.Anything).
		Return(fmt.Errorf("%w: duplicate key", repositories.ErrDuplicateDeposit)).Once()
	recorded := &models.Transaction{
		ExternalID: "ord_webhook", Type: models.TransactionTypeOfframp, Status: models.TxStatusFundsReceived,
		Provider: models.ProviderBridge, GatewayOrderID: "drn_sim_1", FiatCurrency: models.FiatCurrencyUSD,
	}
	transactions.EXPECT().FindByProviderAndDepositID(models.ProviderBridge, "drn_sim_1").Return(recorded, nil).Once()

	view, err := h.service.SimulateDeposit(context.Background(), payoutUser(), payoutCustomer(), simulateInput())
	require.NoError(t, err)
	require.Equal(t, "ord_webhook", view.ID)
}
