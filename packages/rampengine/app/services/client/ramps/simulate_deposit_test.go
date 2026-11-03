package ramps_test

import (
	"context"
	"strings"
	"testing"

	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/ramps"
)

func TestSimulateDepositCompletesAwaitingFundsOrder(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)

	mode := ramps.DepositModeSimulated
	tx := &models.Transaction{
		CustomerID: userID,
		UserID:     userID, Provider: models.ProviderBridge,
		Type: models.TransactionTypeOfframp, ExternalID: "ord-sim-1",
		Status: models.TxStatusAwaitingFunds, AmountFiat: "95.00", AmountToken: "18.500000",
		TargetToken: models.TargetTokenUSDC, GatewayOrderID: "trf_stub",
		DepositMode: &mode,
	}
	h.txRepo.EXPECT().FindByCustomerAndExternalID(userID, "ord-sim-1").Return(tx, nil)
	h.txRepo.EXPECT().Update(testifymock.MatchedBy(func(row *models.Transaction) bool {
		return row.DepositTxSignature != nil &&
			strings.HasPrefix(*row.DepositTxSignature, "sandbox-simulated:")
	})).Return(nil).Once()
	h.txRepo.EXPECT().Update(testifymock.MatchedBy(func(row *models.Transaction) bool {
		return row.Status == models.TxStatusCompleted && row.CompletedAt != nil
	})).Return(nil).Once()
	h.broadcaster.EXPECT().Publish(userID, testifymock.Anything).Once()

	view, err := h.offramp.SimulateDeposit(context.Background(), userID, "ord-sim-1")
	require.NoError(t, err)
	require.Equal(t, models.TxStatusCompleted, view.Status)
	require.Equal(t, ramps.DepositModeSimulated, view.DepositMode)
}

func TestSimulateDepositRejectsProductionMode(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	h.config(models.ProviderBridge, true, models.ProviderModeProduction)

	tx := &models.Transaction{
		CustomerID: userID,
		UserID:     userID, Provider: models.ProviderBridge,
		Type: models.TransactionTypeOfframp, ExternalID: "ord-prod-1",
		Status: models.TxStatusAwaitingFunds, GatewayOrderID: "trf_stub",
	}
	h.txRepo.EXPECT().FindByCustomerAndExternalID(userID, "ord-prod-1").Return(tx, nil)

	_, err := h.offramp.SimulateDeposit(context.Background(), userID, "ord-prod-1")
	require.ErrorIs(t, err, ramp.ErrSandboxOnly)
}

func TestSimulateDepositRejectsWhenAppEnvProduction(t *testing.T) {
	h := newHarnessWithAppEnv(t, "production", models.RailPixPayout, models.ProviderBridge)

	mode := ramps.DepositModeSimulated
	tx := &models.Transaction{
		CustomerID: userID,
		UserID:     userID, Provider: models.ProviderBridge,
		Type: models.TransactionTypeOfframp, ExternalID: "ord-prod-env-1",
		Status: models.TxStatusAwaitingFunds, GatewayOrderID: "trf_stub",
		DepositMode: &mode,
	}
	h.txRepo.EXPECT().FindByCustomerAndExternalID(userID, "ord-prod-env-1").Return(tx, nil)

	_, err := h.offramp.SimulateDeposit(context.Background(), userID, "ord-prod-env-1")
	require.ErrorIs(t, err, ramp.ErrSandboxOnly)
}

func TestSimulateDepositRejectsWhenOrderDepositModeOnChain(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)

	mode := ramps.DepositModeOnChain
	tx := &models.Transaction{
		CustomerID: userID,
		UserID:     userID, Provider: models.ProviderBridge,
		Type: models.TransactionTypeOfframp, ExternalID: "ord-onchain-1",
		Status: models.TxStatusAwaitingFunds, GatewayOrderID: "trf_stub",
		DepositMode: &mode,
	}
	h.txRepo.EXPECT().FindByCustomerAndExternalID(userID, "ord-onchain-1").Return(tx, nil)

	_, err := h.offramp.SimulateDeposit(context.Background(), userID, "ord-onchain-1")
	require.ErrorIs(t, err, ramp.ErrSandboxOnly)
}

func TestSimulateDepositRejectsOrderNotFound(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	h.txRepo.EXPECT().FindByCustomerAndExternalID(userID, "ord-missing").Return(nil, nil).Once()

	_, err := h.offramp.SimulateDeposit(context.Background(), userID, "ord-missing")
	require.ErrorIs(t, err, ramps.ErrOrderNotFound)
}

func TestSimulateDepositRejectsWrongTransactionType(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	tx := &models.Transaction{
		CustomerID: userID,
		UserID:     userID, Provider: models.ProviderBridge,
		Type: models.TransactionTypeOnramp, ExternalID: "ord-onramp-1",
		Status: models.TxStatusAwaitingFunds, GatewayOrderID: "trf_stub",
	}
	h.txRepo.EXPECT().FindByCustomerAndExternalID(userID, "ord-onramp-1").Return(tx, nil).Once()

	_, err := h.offramp.SimulateDeposit(context.Background(), userID, "ord-onramp-1")
	require.ErrorIs(t, err, ramps.ErrOrderNotFound)
}

func TestSimulateDepositRejectsInvalidOrderState(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)

	mode := ramps.DepositModeSimulated
	tx := &models.Transaction{
		CustomerID: userID,
		UserID:     userID, Provider: models.ProviderBridge,
		Type: models.TransactionTypeOfframp, ExternalID: "ord-processing-1",
		Status: models.TxStatusProcessing, GatewayOrderID: "trf_stub",
		DepositMode: &mode,
	}
	h.txRepo.EXPECT().FindByCustomerAndExternalID(userID, "ord-processing-1").Return(tx, nil).Once()

	_, err := h.offramp.SimulateDeposit(context.Background(), userID, "ord-processing-1")
	require.ErrorIs(t, err, ramps.ErrInvalidOrderState)
	h.txRepo.AssertNotCalled(t, "Update", testifymock.Anything)
	h.broadcaster.AssertNotCalled(t, "Publish", testifymock.Anything, testifymock.Anything)
}

func TestSimulateDepositIsIdempotentForSimulatedCompletion(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)

	sig := "sandbox-simulated:already-done"
	mode := ramps.DepositModeSimulated
	tx := &models.Transaction{
		CustomerID: userID,
		UserID:     userID, Provider: models.ProviderBridge,
		Type: models.TransactionTypeOfframp, ExternalID: "ord-replay-1",
		Status: models.TxStatusCompleted, AmountFiat: "95.00", AmountToken: "18.500000",
		TargetToken: models.TargetTokenUSDC, GatewayOrderID: "trf_stub",
		DepositMode: &mode, DepositTxSignature: &sig,
	}
	h.txRepo.EXPECT().FindByCustomerAndExternalID(userID, "ord-replay-1").Return(tx, nil).Once()

	view, err := h.offramp.SimulateDeposit(context.Background(), userID, "ord-replay-1")
	require.NoError(t, err)
	require.Equal(t, models.TxStatusCompleted, view.Status)
	require.Equal(t, ramps.DepositModeSimulated, view.DepositMode)
	h.txRepo.AssertNotCalled(t, "Update", testifymock.Anything)
	h.broadcaster.AssertNotCalled(t, "Publish", testifymock.Anything, testifymock.Anything)
}

func TestSimulateDepositAllowsLegacyRowAfterProviderSwitchedToProduction(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	h.config(models.ProviderBridge, true, models.ProviderModeProduction)

	mode := ramps.DepositModeSimulated
	tx := &models.Transaction{
		CustomerID: userID,
		UserID:     userID, Provider: models.ProviderBridge,
		Type: models.TransactionTypeOfframp, ExternalID: "ord-legacy-sim-1",
		Status: models.TxStatusAwaitingFunds, AmountFiat: "95.00", AmountToken: "18.500000",
		TargetToken: models.TargetTokenUSDC, GatewayOrderID: "trf_stub",
		DepositMode: &mode,
	}
	h.txRepo.EXPECT().FindByCustomerAndExternalID(userID, "ord-legacy-sim-1").Return(tx, nil)
	h.txRepo.EXPECT().Update(testifymock.Anything).Return(nil).Twice()
	h.broadcaster.EXPECT().Publish(userID, testifymock.Anything).Once()

	view, err := h.offramp.SimulateDeposit(context.Background(), userID, "ord-legacy-sim-1")
	require.NoError(t, err)
	require.Equal(t, models.TxStatusCompleted, view.Status)
	require.Equal(t, ramps.DepositModeSimulated, view.DepositMode)
}
