package ramp_test

import (
	"context"
	"testing"
	"time"

	"github.com/goravel/framework/database/orm"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	rampsvc "github.com/macrowallets/waas/packages/rampengine/app/services/ramp"
	"github.com/macrowallets/waas/packages/rampengine/tests/support"
)

// liquidationAddressRow is the ACH address a drain resolves to: it carries the
// user, the bank account, the token and the fee the payout was opened with.
func liquidationAddressRow() *models.UserLiquidationAddress {
	return &models.UserLiquidationAddress{
		Model: orm.Model{ID: 5}, CustomerID: 7, UserID: 7, Provider: models.ProviderBridge, UserBankAccountID: 12,
		Rail: models.RailUSDACHPayout, Chain: "solana", Currency: "usdc",
		ExternalAddressID: "la_1", Address: "BridgeLiquidationAddr1", ReturnAddress: "So1Wa11et",
		FeePercent: support.Ptr("1.00"), Status: models.LiquidationAddressStatusActive, RawPayload: "{}",
	}
}

// drainEvent is a drain as the adapter normalized it: everything is on Drain.
func drainEvent(drain *ramp.LiquidationDrain) *ramp.WebhookResult {
	return &ramp.WebhookResult{
		Kind:              ramp.WebhookKindLiquidationDrain,
		EventID:           "evt_1",
		EventType:         "liquidation_address.drain.updated.status_transitioned",
		ProviderAccountID: drain.ProviderAddressID,
		Drain:             drain,
	}
}

func fundsReceivedDrain() *ramp.LiquidationDrain {
	occurred := time.Date(2026, 10, 14, 18, 2, 11, 0, time.UTC)
	return &ramp.LiquidationDrain{
		DrainID: "drn_1", ProviderAddressID: "la_1", Status: models.TxStatusFundsReceived,
		SourceAmount: "250.00", SourceCurrency: "usdc", PaymentRail: "ach",
		DepositTxHash: "5xQySolanaSignature", FromAddress: "So1Wa11et",
		OccurredAt: &occurred, Raw: []byte(`{"id":"drn_1"}`),
	}
}

// A company's address opens the company's payout, with the member who
// provisioned it as the actor.
func TestApplyLiquidationDrainOpensThePayoutOfTheAddressCustomer(t *testing.T) {
	address := liquidationAddressRow()
	address.CustomerID = 1_000_000_000_007
	tx := &models.Transaction{}
	rampsvc.ApplyLiquidationDrain(tx, address, fundsReceivedDrain())

	require.Equal(t, uint(1_000_000_000_007), tx.CustomerID)
	require.Equal(t, uint(7), tx.UserID)
}

// The first report of a drain is the whole identity of the payout: the address
// names the customer, the user, the rail, the token and the bank account.
func TestApplyLiquidationDrainOpensTheOfframp(t *testing.T) {
	tx := &models.Transaction{}
	rampsvc.ApplyLiquidationDrain(tx, liquidationAddressRow(), fundsReceivedDrain())

	require.Equal(t, uint(7), tx.CustomerID)
	require.Equal(t, uint(7), tx.UserID)
	require.Equal(t, models.ProviderBridge, tx.Provider)
	require.Equal(t, models.TransactionTypeOfframp, tx.Type)
	require.Equal(t, models.RailUSDACHPayout, *tx.Rail)
	require.Equal(t, models.FiatCurrencyUSD, tx.FiatCurrency)
	require.NotEmpty(t, tx.ExternalID)
	require.Equal(t, "drn_1", tx.GatewayOrderID)
	require.Equal(t, "drn_1", *tx.DepositID)
	require.Equal(t, "USDC", tx.TargetToken)
	require.Equal(t, "solana", *tx.Chain)
	require.Equal(t, "BridgeLiquidationAddr1", tx.DestinationWallet)
	require.Equal(t, uint(5), *tx.LiquidationAddressID)
	require.Equal(t, uint(12), *tx.BankAccountID)
	require.Equal(t, "1.00", *tx.FeePercent)
	require.Equal(t, models.TxStatusFundsReceived, tx.Status)
	require.Equal(t, "250.00", tx.AmountToken)
	require.Equal(t, "247.50", tx.AmountFiat, "nothing was sent yet: the estimate, token less the 1% fee")
	require.Nil(t, tx.FeeAmount)
	require.Equal(t, "ach", *tx.PaymentRail)
	require.Equal(t, "So1Wa11et", *tx.FromAddress)
	require.Equal(t, "5xQySolanaSignature", *tx.DepositTxSignature)
	require.JSONEq(t, `{"id":"drn_1"}`, tx.RawPayload)
	require.Nil(t, tx.ExpiresAt, "the crypto arrived before the row: no payment window")
	require.Nil(t, tx.CompletedAt)
}

// A later report carries the receipt and may omit what the first said; what it
// omits is kept, and an existing row keeps its identity.
func TestApplyLiquidationDrainKeepsWhatALaterReportOmits(t *testing.T) {
	address := liquidationAddressRow()
	tx := &models.Transaction{}
	rampsvc.ApplyLiquidationDrain(tx, address, fundsReceivedDrain())
	externalID := tx.ExternalID

	settled := time.Date(2026, 10, 15, 8, 31, 0, 0, time.UTC)
	rampsvc.ApplyLiquidationDrain(tx, address, &ramp.LiquidationDrain{
		DrainID: "drn_1", Status: models.TxStatusCompleted,
		DestinationAmount: "247.50", FeeAmount: "2.50", OccurredAt: &settled,
	})

	require.Equal(t, externalID, tx.ExternalID)
	require.Equal(t, models.TxStatusCompleted, tx.Status)
	require.Equal(t, "250.00", tx.AmountToken)
	require.Equal(t, "247.50", tx.AmountFiat)
	require.Equal(t, "2.50", *tx.FeeAmount)
	require.Equal(t, "usdc", *tx.FeeCurrency, "Bridge takes the developer fee in the token, before conversion")
	require.Equal(t, "5xQySolanaSignature", *tx.DepositTxSignature)
	require.True(t, tx.CompletedAt.Equal(settled))

	require.False(t, rampsvc.DrainChangedFromProvider(tx, address, &ramp.LiquidationDrain{
		DrainID: "drn_1", Status: models.TxStatusCompleted, DestinationAmount: "247.5",
	}), "the same amount written differently is no change")
	require.True(t, rampsvc.DrainChangedFromProvider(tx, address, &ramp.LiquidationDrain{
		DrainID: "drn_1", Status: models.TxStatusRefunded,
	}))
}

func TestProcessCreatesTheOfframpFromTheDrainEvent(t *testing.T) {
	h := newHarness(t)
	row := eventRow()

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).
		Return(drainEvent(fundsReceivedDrain()), nil).Once()
	h.addresses.EXPECT().FindByProviderAndExternalID(models.ProviderBridge, "la_1").
		Return(liquidationAddressRow(), nil).Once()
	h.db.EXPECT().FindDepositForUpdate(models.ProviderBridge, "drn_1").Return(nil, nil).Once()
	var created *models.Transaction
	h.db.EXPECT().CreateTransaction(testifymock.Anything).RunAndReturn(func(tx *models.Transaction) error {
		tx.ID = 42
		created = tx
		return nil
	}).Once()
	h.broadcaster.EXPECT().Publish(uint(7), testifymock.Anything).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.Equal(t, models.TransactionTypeOfframp, created.Type)
	require.Equal(t, models.RailUSDACHPayout, *created.Rail)
	require.Equal(t, "drn_1", *created.DepositID)
	require.Equal(t, uint(5), *created.LiquidationAddressID)
	require.Equal(t, uint(42), *row.TransactionID)
	require.Equal(t, []string{models.TxStatusFundsReceived}, h.announced)
}

// Crypto drained from an address we do not know is an operational anomaly:
// it goes to processing_error, where an operator finds it.
func TestProcessErrorsOnADrainOfAnUnknownAddress(t *testing.T) {
	h := newHarness(t)
	row := eventRow()

	h.storedEvent(row)
	drain := fundsReceivedDrain()
	drain.ProviderAddressID = "la_x"
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(drainEvent(drain), nil).Once()
	h.addresses.EXPECT().FindByProviderAndExternalID(models.ProviderBridge, "la_x").Return(nil, nil).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.Error(t, h.processor.Process(context.Background(), eventRowID))
	require.NotNil(t, row.ProcessingError)
	require.Contains(t, *row.ProcessingError, "no bridge liquidation address la_x")
	h.db.AssertNotCalled(t, "CreateTransaction", testifymock.Anything)
}

// A new drain on an inactive address — its bank account or its user was
// deleted — opens no payout: Bridge may still have paid an account the user
// removed, which XIP must not show as an ordinary payout. It goes to
// processing_error, like an unknown address, where an operator reviews it.
func TestProcessHoldsANewDrainOfAnInactiveAddressForReview(t *testing.T) {
	h := newHarness(t)
	row := eventRow()
	inactive := liquidationAddressRow()
	inactive.Status = models.LiquidationAddressStatusInactive

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).
		Return(drainEvent(fundsReceivedDrain()), nil).Once()
	h.addresses.EXPECT().FindByProviderAndExternalID(models.ProviderBridge, "la_1").Return(inactive, nil).Once()
	h.db.EXPECT().FindDepositForUpdate(models.ProviderBridge, "drn_1").Return(nil, nil).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.Error(t, h.processor.Process(context.Background(), eventRowID))
	require.NotNil(t, row.ProcessingError)
	require.Contains(t, *row.ProcessingError, "inactive liquidation address 5")
	h.db.AssertNotCalled(t, "CreateTransaction", testifymock.Anything)
}

// A payout that started before the address went inactive keeps moving: it is
// money already on its way, and the user sees it settle.
func TestProcessMovesADrainStartedBeforeItsAddressWentInactive(t *testing.T) {
	h := newHarness(t)
	row := eventRow()
	inactive := liquidationAddressRow()
	inactive.Status = models.LiquidationAddressStatusInactive
	existing := &models.Transaction{
		Model: orm.Model{ID: 42}, CustomerID: 7, UserID: 7, Provider: models.ProviderBridge,
		Type: models.TransactionTypeOfframp, ExternalID: "ext_1", GatewayOrderID: "drn_1",
		DepositID: support.Ptr("drn_1"), Status: models.TxStatusAwaitingFunds,
		AmountFiat: "0", AmountToken: "250.00", TargetToken: "USDC",
	}

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).
		Return(drainEvent(fundsReceivedDrain()), nil).Once()
	h.addresses.EXPECT().FindByProviderAndExternalID(models.ProviderBridge, "la_1").Return(inactive, nil).Once()
	h.db.EXPECT().FindDepositForUpdate(models.ProviderBridge, "drn_1").Return(existing, nil).Once()
	h.db.EXPECT().UpdateTransaction(existing).Return(nil).Once()
	h.broadcaster.EXPECT().Publish(uint(7), testifymock.Anything).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.Equal(t, models.TxStatusFundsReceived, existing.Status)
}

// Delivery is out of order: a funds_received arriving after the payout
// settled must not walk it back.
func TestProcessRefusesALateDrainReportOnACompletedOfframp(t *testing.T) {
	h := newHarness(t)
	row := eventRow()
	existing := &models.Transaction{
		Model: orm.Model{ID: 42}, CustomerID: 7, UserID: 7, Provider: models.ProviderBridge,
		Type: models.TransactionTypeOfframp, ExternalID: "ext_1", GatewayOrderID: "drn_1",
		DepositID: support.Ptr("drn_1"), Status: models.TxStatusCompleted,
		AmountFiat: "247.50", AmountToken: "250.00", TargetToken: "USDC",
	}

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).
		Return(drainEvent(fundsReceivedDrain()), nil).Once()
	h.addresses.EXPECT().FindByProviderAndExternalID(models.ProviderBridge, "la_1").
		Return(liquidationAddressRow(), nil).Once()
	h.db.EXPECT().FindDepositForUpdate(models.ProviderBridge, "drn_1").Return(existing, nil).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.Equal(t, models.TxStatusCompleted, existing.Status)
	h.db.AssertNotCalled(t, "UpdateTransaction", testifymock.Anything)
}

// The bank returned a payout Bridge had already paid out: returned comes after
// payment_processed, and the payout is processing again (the money is on its
// way back) instead of staying completed; refund_failed after it is failed.
// The event records the transaction it moved.
func TestProcessMovesACompletedOfframpTheBankReturned(t *testing.T) {
	for name, tc := range map[string]struct{ status, want string }{
		"returned":      {models.TxStatusProcessing, models.TxStatusProcessing},
		"refund_failed": {models.TxStatusFailed, models.TxStatusFailed},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			row := eventRow()
			existing := &models.Transaction{
				Model: orm.Model{ID: 42}, CustomerID: 7, UserID: 7, Provider: models.ProviderBridge,
				Type: models.TransactionTypeOfframp, ExternalID: "ext_1", GatewayOrderID: "drn_1",
				DepositID: support.Ptr("drn_1"), Status: models.TxStatusCompleted,
				AmountFiat: "247.50", AmountToken: "250.00", TargetToken: "USDC",
			}
			drain := fundsReceivedDrain()
			drain.Status, drain.Returning, drain.FailureReason = tc.status, true, "R03"

			h.storedEvent(row)
			h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).
				Return(drainEvent(drain), nil).Once()
			h.addresses.EXPECT().FindByProviderAndExternalID(models.ProviderBridge, "la_1").
				Return(liquidationAddressRow(), nil).Once()
			h.db.EXPECT().FindDepositForUpdate(models.ProviderBridge, "drn_1").Return(existing, nil).Once()
			h.db.EXPECT().UpdateTransaction(existing).Return(nil).Once()
			h.broadcaster.EXPECT().Publish(uint(7), testifymock.Anything).Once()
			h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

			require.NoError(t, h.processor.Process(context.Background(), eventRowID))
			require.Equal(t, tc.want, existing.Status)
			require.Equal(t, "R03", *existing.FailureReason)
			require.Equal(t, uint(42), *row.TransactionID)
		})
	}
}

// A returned payout (processing again, with the CompletedAt of its
// payment_processed) is not flipped back to completed by a redelivered
// payment_processed; refunded still moves it.
func TestProcessKeepsAReturnedOfframpFromALatePaymentProcessed(t *testing.T) {
	h := newHarness(t)
	row := eventRow()
	completed := time.Date(2026, 10, 15, 8, 31, 0, 0, time.UTC)
	existing := &models.Transaction{
		Model: orm.Model{ID: 42}, CustomerID: 7, UserID: 7, Provider: models.ProviderBridge,
		Type: models.TransactionTypeOfframp, ExternalID: "ext_1", GatewayOrderID: "drn_1",
		DepositID: support.Ptr("drn_1"), Status: models.TxStatusProcessing, CompletedAt: &completed,
		AmountFiat: "247.50", AmountToken: "250.00", TargetToken: "USDC",
	}
	drain := fundsReceivedDrain()
	drain.Status = models.TxStatusCompleted

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(drainEvent(drain), nil).Once()
	h.addresses.EXPECT().FindByProviderAndExternalID(models.ProviderBridge, "la_1").Return(liquidationAddressRow(), nil).Once()
	h.db.EXPECT().FindDepositForUpdate(models.ProviderBridge, "drn_1").Return(existing, nil).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.Equal(t, models.TxStatusProcessing, existing.Status)
	h.db.AssertNotCalled(t, "UpdateTransaction", testifymock.Anything)
}

func TestDrainMayMove(t *testing.T) {
	completed := time.Now()
	returned := &models.Transaction{Type: models.TransactionTypeOfframp, Status: models.TxStatusProcessing, CompletedAt: &completed}
	fresh := &models.Transaction{Type: models.TransactionTypeOfframp, Status: models.TxStatusProcessing}

	require.False(t, rampsvc.DrainMayMove(returned, &ramp.LiquidationDrain{Status: models.TxStatusCompleted}))
	require.False(t, rampsvc.DrainMayMove(returned, &ramp.LiquidationDrain{Status: models.TxStatusProcessing}))
	require.True(t, rampsvc.DrainMayMove(returned, &ramp.LiquidationDrain{Status: models.TxStatusRefunded}))
	require.True(t, rampsvc.DrainMayMove(returned, &ramp.LiquidationDrain{Status: models.TxStatusFailed, Returning: true}))
	require.True(t, rampsvc.DrainMayMove(fresh, &ramp.LiquidationDrain{Status: models.TxStatusCompleted}))
}

// The bank can return a payout before Bridge's payment_processed reaches us.
// The return's reason is what the row keeps of it (CompletedAt is still
// empty), and it is as sticky as a return after completion: a late
// payment_processed or payment_submitted does not move it, refunded and failed
// do.
func TestDrainMayMoveKeepsAPayoutReturnedBeforeItCompleted(t *testing.T) {
	returned := &models.Transaction{
		Type: models.TransactionTypeOfframp, Status: models.TxStatusProcessing, FailureReason: support.Ptr("bank_rejected"),
	}

	require.False(t, rampsvc.DrainMayMove(returned, &ramp.LiquidationDrain{Status: models.TxStatusCompleted}))
	require.False(t, rampsvc.DrainMayMove(returned, &ramp.LiquidationDrain{Status: models.TxStatusProcessing}))
	require.True(t, rampsvc.DrainMayMove(returned, &ramp.LiquidationDrain{Status: models.TxStatusProcessing, Returning: true}))
	require.True(t, rampsvc.DrainMayMove(returned, &ramp.LiquidationDrain{Status: models.TxStatusRefunded}))
	require.True(t, rampsvc.DrainMayMove(returned, &ramp.LiquidationDrain{Status: models.TxStatusFailed, Returning: true}))
}

func TestDrainCanLeaveTerminal(t *testing.T) {
	returned := &ramp.LiquidationDrain{Status: models.TxStatusProcessing, Returning: true}
	late := &ramp.LiquidationDrain{Status: models.TxStatusProcessing}
	refunded := &ramp.LiquidationDrain{Status: models.TxStatusRefunded}

	require.True(t, rampsvc.DrainCanLeaveTerminal(models.TxStatusCompleted, returned))
	require.True(t, rampsvc.DrainCanLeaveTerminal(models.TxStatusCompleted, refunded))
	require.False(t, rampsvc.DrainCanLeaveTerminal(models.TxStatusCompleted, late), "a late report is not a return")
	require.False(t, rampsvc.DrainCanLeaveTerminal(models.TxStatusFailed, returned), "only a paid-out payout comes back")
}

// A closed payout rail stops a drain from opening a transaction; the event is
// accepted, so Bridge does not redeliver it forever.
func TestProcessDoesNotOpenAnOfframpWhenTheUSDPayoutRailIsClosed(t *testing.T) {
	features := openFeatures()
	require.NoError(t, features.Deactivate(context.Background(), ramp.RailUSDACHPayout.Flag(), nil))
	h := newHarnessWithFeatures(t, features)
	row := eventRow()

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).
		Return(drainEvent(fundsReceivedDrain()), nil).Once()
	h.addresses.EXPECT().FindByProviderAndExternalID(models.ProviderBridge, "la_1").
		Return(liquidationAddressRow(), nil).Once()
	h.db.EXPECT().FindDepositForUpdate(models.ProviderBridge, "drn_1").Return(nil, nil).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	h.db.AssertNotCalled(t, "CreateTransaction", testifymock.Anything)
}
