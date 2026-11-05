package ramps_test

import (
	"context"
	"testing"
	"time"

	"github.com/goravel/framework/database/orm"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
	"github.com/macrowallets/waas/packages/rampengine/tests/support"
)

// A USD payout in flight is re-read out of its address's drains when the user
// looks at it, the way a deposit is re-read out of its account's history.
func TestLatestRefreshesADrainThroughItsAddress(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderBridge)
	payout := &payoutAdapter{
		MockProviderAdapter:           h.adapter,
		MockLiquidationAddressAdapter: mocks.NewMockLiquidationAddressAdapter(t),
	}
	addresses := mocks.NewMockUserLiquidationAddressRepository(t)
	d := h.deps
	d.Factory = ramp.NewFactory(payout)
	d.LiquidationAddresses = addresses
	transactions := ramps.NewTransactionService(d)

	rail, addressID := models.RailUSDACHPayout, uint(5)
	tx := &models.Transaction{
		Model: orm.Model{ID: 1}, CustomerID: userID, UserID: userID, Provider: models.ProviderBridge,
		Type: models.TransactionTypeOfframp, ExternalID: orderID, GatewayOrderID: "drn_1",
		DepositID: support.Ptr("drn_1"), Status: models.TxStatusFundsReceived, Rail: &rail,
		LiquidationAddressID: &addressID, FiatCurrency: models.FiatCurrencyUSD,
		AmountFiat: "0", AmountToken: "25.00", TargetToken: "USDC",
		DepositMode: support.Ptr(ramps.DepositModeOnChain),
	}
	h.txRepo.EXPECT().FindLatestByCustomer(userID).Return(tx, nil).Once()
	addresses.EXPECT().FindByID(addressID).Return(activeAddress(rail), nil).Once()
	payout.MockLiquidationAddressAdapter.EXPECT().GetDrain(testifymock.Anything, bridgeRef, "la_1", "drn_1").
		Return(&ramp.LiquidationDrain{
			DrainID: "drn_1", Status: models.TxStatusCompleted, DestinationAmount: "24.75", FeeAmount: "0.25",
		}, nil).Once()
	h.txRepo.EXPECT().SaveProviderSnapshotIfStatus(tx, models.TxStatusFundsReceived).Return(true, nil).Once()
	h.broadcaster.EXPECT().Publish(userID, testifymock.Anything).Once()

	view, err := transactions.Latest(context.Background(), userID)
	require.NoError(t, err)
	require.Equal(t, models.TxStatusCompleted, view.Status)
	require.Equal(t, "24.75", view.AmountFiat)
}

// A payout the bank returned after paying it is processing again with the
// CompletedAt of its payment_processed. Looking at it re-reads the drain, and
// a drain read back as payment_processed must not walk it back to completed,
// as the webhook and the reconciler already refuse (rampsvc.DrainMayMove).
func TestLatestKeepsAReturnedDrainFromALatePaymentProcessed(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderBridge)
	payout := &payoutAdapter{
		MockProviderAdapter:           h.adapter,
		MockLiquidationAddressAdapter: mocks.NewMockLiquidationAddressAdapter(t),
	}
	addresses := mocks.NewMockUserLiquidationAddressRepository(t)
	d := h.deps
	d.Factory = ramp.NewFactory(payout)
	d.LiquidationAddresses = addresses
	transactions := ramps.NewTransactionService(d)

	rail, addressID := models.RailUSDACHPayout, uint(5)
	completed := time.Date(2026, 10, 20, 7, 7, 12, 0, time.UTC)
	tx := &models.Transaction{
		Model: orm.Model{ID: 1}, CustomerID: userID, UserID: userID, Provider: models.ProviderBridge,
		Type: models.TransactionTypeOfframp, ExternalID: orderID, GatewayOrderID: "drn_1",
		DepositID: support.Ptr("drn_1"), Status: models.TxStatusProcessing, CompletedAt: &completed,
		Rail: &rail, LiquidationAddressID: &addressID, FiatCurrency: models.FiatCurrencyUSD,
		AmountFiat: "24.75", AmountToken: "25.00", TargetToken: "USDC",
		FailureReason: support.Ptr("account_closed"),
		DepositMode:   support.Ptr(ramps.DepositModeOnChain),
	}
	h.txRepo.EXPECT().FindLatestByCustomer(userID).Return(tx, nil).Once()
	addresses.EXPECT().FindByID(addressID).Return(activeAddress(rail), nil).Once()
	payout.MockLiquidationAddressAdapter.EXPECT().GetDrain(testifymock.Anything, bridgeRef, "la_1", "drn_1").
		Return(&ramp.LiquidationDrain{
			DrainID: "drn_1", Status: models.TxStatusCompleted, DestinationAmount: "24.75", FeeAmount: "0.25",
		}, nil).Once()

	view, err := transactions.Latest(context.Background(), userID)
	require.NoError(t, err)
	require.Equal(t, models.TxStatusProcessing, view.Status)
	h.txRepo.AssertNotCalled(t, "SaveProviderSnapshotIfStatus", testifymock.Anything, testifymock.Anything)
}
