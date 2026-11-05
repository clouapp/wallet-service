package ramps_test

import (
	"context"
	"errors"
	"testing"

	"github.com/goravel/framework/database/orm"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/tests/support"
)

const orderID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

func storedOrder(txType, status, gatewayOrderID string) *models.Transaction {
	rail := models.RailPixPayin
	if txType == models.TransactionTypeOfframp {
		rail = models.RailPixPayout
	}
	return &models.Transaction{
		Model: orm.Model{ID: 1}, CustomerID: userID, UserID: userID, Provider: models.ProviderBridge,
		Type: txType, ExternalID: orderID, GatewayOrderID: gatewayOrderID,
		Status: status, Rail: &rail,
		AmountFiat: "100.00", AmountToken: "18.222500", TargetToken: "usdc",
	}
}

// The lookup is scoped by the JWT's user, never by an id the client supplies
// alone. This is the whole read-side ownership guarantee.
func TestShowScopesTheLookupToTheCaller(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderBridge)
	h.txRepo.EXPECT().FindByCustomerAndExternalID(userID, orderID).Return(nil, nil).Once()

	_, err := h.onramp.Show(context.Background(), userID, orderID)
	require.ErrorIs(t, err, ramps.ErrOrderNotFound)
}

// An off-ramp id must not surface through the on-ramp endpoint dressed as one.
// It reads as absent rather than as a mismatch: telling them apart would let a
// caller use the endpoint to learn which direction an id belongs to.
func TestShowTreatsTheOtherDirectionAsAbsent(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderBridge)
	h.txRepo.EXPECT().FindByCustomerAndExternalID(userID, orderID).
		Return(storedOrder(models.TransactionTypeOfframp, models.TxStatusAwaitingFunds, "trf_1"), nil).Once()

	_, err := h.onramp.Show(context.Background(), userID, orderID)
	require.ErrorIs(t, err, ramps.ErrOrderNotFound)
	h.adapter.AssertNotCalled(t, "GetOrder",
		testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// Webhooks alone are not a status machine. The read path is the second source
// of truth, so an order still in flight is reconciled before it is answered.
func TestShowRefreshesAnInFlightOrder(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderBridge)
	tx := storedOrder(models.TransactionTypeOnramp, models.TxStatusAwaitingFunds, "trf_1")

	h.txRepo.EXPECT().FindByCustomerAndExternalID(userID, orderID).Return(tx, nil).Once()
	h.adapter.EXPECT().GetOrder(testifymock.Anything, testifymock.Anything, "trf_1").
		Return(&ramp.RampOrder{Status: models.TxStatusCompleted, TxHash: "0xabc"}, nil).Once()
	h.txRepo.EXPECT().SaveProviderSnapshotIfStatus(tx, models.TxStatusAwaitingFunds).Return(true, nil).Once()
	h.broadcaster.EXPECT().Publish(userID, testifymock.Anything).Once()

	view, err := h.onramp.Show(context.Background(), userID, orderID)
	require.NoError(t, err)
	require.Equal(t, models.TxStatusCompleted, view.Status)
	require.Equal(t, "0xabc", *tx.DepositTxSignature)
	require.Equal(t, []string{models.TxStatusCompleted}, h.announced,
		"the read saw the provider end the order, and the devices hear it as they would from the event")
}

// A settled order is not asked about again — the answer cannot change and the
// call would be a provider round trip per screen view.
func TestShowDoesNotRefreshATerminalOrder(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderBridge)
	tx := storedOrder(models.TransactionTypeOnramp, models.TxStatusCompleted, "trf_1")
	h.txRepo.EXPECT().FindByCustomerAndExternalID(userID, orderID).Return(tx, nil).Once()

	view, err := h.onramp.Show(context.Background(), userID, orderID)
	require.NoError(t, err)
	require.Equal(t, models.TxStatusCompleted, view.Status)
	h.adapter.AssertNotCalled(t, "GetOrder",
		testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// A reservation row the provider never answered has no id to ask under. The
// reconciler expires it; the read just returns what is stored.
func TestShowDoesNotRefreshAnOrderWithNoGatewayID(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderBridge)
	tx := storedOrder(models.TransactionTypeOnramp, models.TxStatusPending, "")
	h.txRepo.EXPECT().FindByCustomerAndExternalID(userID, orderID).Return(tx, nil).Once()

	view, err := h.onramp.Show(context.Background(), userID, orderID)
	require.NoError(t, err)
	require.Equal(t, models.TxStatusPending, view.Status)
	h.adapter.AssertNotCalled(t, "GetOrder",
		testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// The persisted row is still a correct answer, just an older one. Failing the
// read would turn a provider hiccup into a broken screen.
func TestShowSwallowsAProviderFailure(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderBridge)
	tx := storedOrder(models.TransactionTypeOnramp, models.TxStatusAwaitingFunds, "trf_1")

	h.txRepo.EXPECT().FindByCustomerAndExternalID(userID, orderID).Return(tx, nil).Once()
	h.adapter.EXPECT().GetOrder(testifymock.Anything, testifymock.Anything, "trf_1").
		Return(nil, errors.New("bridge: GET /transfers/trf_1: 503")).Once()

	view, err := h.onramp.Show(context.Background(), userID, orderID)
	require.NoError(t, err)
	require.Equal(t, models.TxStatusAwaitingFunds, view.Status)
	h.broadcaster.AssertNotCalled(t, "Publish", testifymock.Anything, testifymock.Anything)
}

// An unchanged status is not news, and publishing it would wake every idle app
// on a poll.
func TestShowDoesNotBroadcastAnUnchangedStatus(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderBridge)
	tx := storedOrder(models.TransactionTypeOnramp, models.TxStatusAwaitingFunds, "trf_1")

	h.txRepo.EXPECT().FindByCustomerAndExternalID(userID, orderID).Return(tx, nil).Once()
	h.adapter.EXPECT().GetOrder(testifymock.Anything, testifymock.Anything, "trf_1").
		Return(&ramp.RampOrder{Status: models.TxStatusAwaitingFunds}, nil).Once()

	_, err := h.onramp.Show(context.Background(), userID, orderID)
	require.NoError(t, err)
	h.txRepo.AssertNotCalled(t, "SaveProviderSnapshotIfStatus", testifymock.Anything, testifymock.Anything)
	h.broadcaster.AssertNotCalled(t, "Publish", testifymock.Anything, testifymock.Anything)
}

func TestRefreshOrderPersistsExchangeRateWithoutStatusChange(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderBridge)
	tx := storedOrder(models.TransactionTypeOfframp, models.TxStatusProcessing, "trf_1")

	h.txRepo.EXPECT().FindByCustomerAndExternalID(userID, orderID).Return(tx, nil).Once()
	h.adapter.EXPECT().GetOrder(testifymock.Anything, testifymock.Anything, "trf_1").
		Return(&ramp.RampOrder{
			Status:  models.TxStatusProcessing,
			Amounts: map[string]string{"exchange_rate": "5.40"},
		}, nil).Once()
	h.txRepo.EXPECT().SaveProviderSnapshotIfStatus(tx, models.TxStatusProcessing).Return(true, nil).Once()
	h.broadcaster.EXPECT().Publish(userID, testifymock.Anything).Once()

	view, err := h.offramp.Show(context.Background(), userID, orderID)
	require.NoError(t, err)
	require.Equal(t, models.TxStatusProcessing, view.Status)
	require.NotNil(t, tx.ExchangeRate)
	require.Equal(t, "5.40", *tx.ExchangeRate)
}

// The off-ramp read goes through the same machinery and must be scoped the same
// way — including refusing an on-ramp id.
func TestOfframpShowTreatsAnOnrampIDAsAbsent(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	h.txRepo.EXPECT().FindByCustomerAndExternalID(userID, orderID).
		Return(storedOrder(models.TransactionTypeOnramp, models.TxStatusAwaitingFunds, "trf_1"), nil).Once()

	_, err := h.offramp.Show(context.Background(), userID, orderID)
	require.ErrorIs(t, err, ramps.ErrOrderNotFound)
}

// A list is a summary screen. Firing one provider call per row to render it
// would multiply upstream load by the page size for information the detail view
// already reconciles.
func TestListDoesNotCallTheProvider(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderBridge)
	rows := []models.Transaction{
		*storedOrder(models.TransactionTypeOnramp, models.TxStatusAwaitingFunds, "trf_1"),
		*storedOrder(models.TransactionTypeOnramp, models.TxStatusCompleted, "trf_2"),
	}
	h.txRepo.EXPECT().Paginate(repositories.TransactionListFilters{
		CustomerID: userID, Type: models.TransactionTypeOnramp, Page: 1, PerPage: 20,
	}).Return(rows, int64(2), nil).Once()

	views, total, err := h.onramp.List(userID, 1, 20)
	require.NoError(t, err)
	require.Equal(t, int64(2), total)
	require.Len(t, views, 2)
	h.adapter.AssertNotCalled(t, "GetOrder",
		testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// The listing is filtered by direction at the repository, so an off-ramp never
// appears under /api/onramps.
func TestListFiltersByDirection(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	h.txRepo.EXPECT().Paginate(repositories.TransactionListFilters{
		CustomerID: userID, Type: models.TransactionTypeOfframp, Page: 2, PerPage: 5,
	}).Return(nil, int64(0), nil).Once()

	views, total, err := h.offramp.List(userID, 2, 5)
	require.NoError(t, err)
	require.Zero(t, total)
	require.Empty(t, views)
}

func TestShowReloadsFromDBAfterLostCAS(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	tx := storedOrder(models.TransactionTypeOfframp, models.TxStatusProcessing, "trf_1")
	reloaded := *tx
	reloaded.AmountFiat = "99.00"

	h.txRepo.EXPECT().FindByCustomerAndExternalID(userID, orderID).Return(tx, nil).Once()
	h.adapter.EXPECT().GetOrder(testifymock.Anything, testifymock.Anything, "trf_1").
		Return(&ramp.RampOrder{
			Status:  models.TxStatusProcessing,
			Amounts: map[string]string{"exchange_rate": "5.40"},
		}, nil).Once()
	h.txRepo.EXPECT().SaveProviderSnapshotIfStatus(tx, models.TxStatusProcessing).Return(false, nil).Once()
	h.txRepo.EXPECT().FindByCustomerAndExternalID(userID, orderID).Return(&reloaded, nil).Once()

	view, err := h.offramp.Show(context.Background(), userID, orderID)
	require.NoError(t, err)
	require.Equal(t, "99.00", view.AmountBRL)
	h.broadcaster.AssertNotCalled(t, "Publish", testifymock.Anything, testifymock.Anything)
}

func TestShowDoesNotPublishWhenProviderSnapshotIsUnchanged(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderBridge)
	fee := "0.277500000000000000"
	instructions := `{"currency": "usdc", "amount": "18.5"}`
	tx := storedOrder(models.TransactionTypeOnramp, models.TxStatusAwaitingFunds, "trf_1")
	tx.FeeAmount = &fee
	tx.DepositInstructions = &instructions

	h.txRepo.EXPECT().FindByCustomerAndExternalID(userID, orderID).Return(tx, nil).Once()
	h.adapter.EXPECT().GetOrder(testifymock.Anything, testifymock.Anything, "trf_1").
		Return(&ramp.RampOrder{
			Status:              models.TxStatusAwaitingFunds,
			FeeAmount:           "0.277500",
			DepositInstructions: map[string]any{"amount": "18.5", "currency": "usdc"},
		}, nil).Once()

	_, err := h.onramp.Show(context.Background(), userID, orderID)
	require.NoError(t, err)
	h.txRepo.AssertNotCalled(t, "SaveProviderSnapshotIfStatus", testifymock.Anything, testifymock.Anything)
	h.broadcaster.AssertNotCalled(t, "Publish", testifymock.Anything, testifymock.Anything)
}

// storedDepositOrder is an on-ramp row the deposit webhook created: the money
// arrived before the row existed, so it carries a standing account and the
// deposit id in GatewayOrderID rather than a transfer id.
func storedDepositOrder(status string) *models.Transaction {
	rail := models.RailPixPayin
	accountID := uint(5)
	return &models.Transaction{
		Model: orm.Model{ID: 2}, CustomerID: userID, UserID: userID, Provider: models.ProviderBridge,
		Type: models.TransactionTypeOnramp, ExternalID: orderID, GatewayOrderID: "dep_1",
		Status: status, Rail: &rail, VirtualAccountID: &accountID,
		AmountFiat: "0", AmountToken: "0", TargetToken: "USDC",
	}
}

// This is the branch TransactionService.Latest uses for the deposit-first
// on-ramp shape (see CLAUDE.md, "The on-ramp has two shapes"): a deposit is
// not a transfer, so it is re-read out of the account's history rather than
// through GetOrder.
func TestLatestRefreshesADepositOrder(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderBridge)
	standing := h.withDepositAdapter(t)
	tx := storedDepositOrder(models.TxStatusFundsReceived)
	account := storedAccount(models.ProviderBridge)

	h.txRepo.EXPECT().FindLatestByCustomer(userID).Return(tx, nil).Once()
	h.vaRepo.EXPECT().FindByID(*tx.VirtualAccountID).Return(account, nil).Once()
	standing.EXPECT().GetDeposit(testifymock.Anything, testifymock.Anything, account.ExternalAccountID, "dep_1").
		Return(&ramp.VirtualAccountDeposit{
			DepositID: "dep_1", ProviderAccountID: account.ExternalAccountID,
			Status: models.TxStatusProcessing, SourceAmount: "100.00", DestinationAmount: "18.20",
		}, nil).Once()
	h.txRepo.EXPECT().SaveProviderSnapshotIfStatus(tx, models.TxStatusFundsReceived).Return(true, nil).Once()
	h.broadcaster.EXPECT().Publish(userID, testifymock.Anything).Once()

	view, err := h.transactions.Latest(context.Background(), userID)
	require.NoError(t, err)
	require.Equal(t, models.TxStatusProcessing, view.Status)
}

// A concurrent writer (webhook, sweep) can update the same deposit between the
// provider read and the write. The lost CAS is not an error: the row is
// reloaded and the reload — not the stale local copy — is what the caller sees.
func TestLatestReloadsFromDBAfterLostCASOnDeposit(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderBridge)
	standing := h.withDepositAdapter(t)
	tx := storedDepositOrder(models.TxStatusFundsReceived)
	account := storedAccount(models.ProviderBridge)
	reloaded := *tx
	reloaded.AmountFiat = "99.00"

	h.txRepo.EXPECT().FindLatestByCustomer(userID).Return(tx, nil).Once()
	h.vaRepo.EXPECT().FindByID(*tx.VirtualAccountID).Return(account, nil).Once()
	standing.EXPECT().GetDeposit(testifymock.Anything, testifymock.Anything, account.ExternalAccountID, "dep_1").
		Return(&ramp.VirtualAccountDeposit{
			DepositID: "dep_1", ProviderAccountID: account.ExternalAccountID,
			Status: models.TxStatusProcessing, SourceAmount: "100.00",
		}, nil).Once()
	h.txRepo.EXPECT().SaveProviderSnapshotIfStatus(tx, models.TxStatusFundsReceived).Return(false, nil).Once()
	h.txRepo.EXPECT().FindByCustomerAndExternalID(userID, orderID).Return(&reloaded, nil).Once()

	view, err := h.transactions.Latest(context.Background(), userID)
	require.NoError(t, err)
	require.Equal(t, "99.00", view.AmountBRL)
	h.broadcaster.AssertNotCalled(t, "Publish", testifymock.Anything, testifymock.Anything)
}

// A row pointing at an account that is gone has nothing left to ask about.
// refreshDepositOrder reports the error, but latest() logs and swallows it —
// the persisted view is still a correct, just older, answer.
func TestLatestSwallowsAMissingVirtualAccount(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderBridge)
	h.withDepositAdapter(t)
	tx := storedDepositOrder(models.TxStatusFundsReceived)

	h.txRepo.EXPECT().FindLatestByCustomer(userID).Return(tx, nil).Once()
	h.vaRepo.EXPECT().FindByID(*tx.VirtualAccountID).Return(nil, nil).Once()

	view, err := h.transactions.Latest(context.Background(), userID)
	require.NoError(t, err)
	require.Equal(t, models.TxStatusFundsReceived, view.Status)
	h.broadcaster.AssertNotCalled(t, "Publish", testifymock.Anything, testifymock.Anything)
}

// Same swallow-and-serve-the-stored-view behavior when the provider read
// itself fails, mirroring the transfer path's TestShowSwallowsAProviderFailure.
func TestLatestSwallowsAProviderFailureOnDeposit(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderBridge)
	standing := h.withDepositAdapter(t)
	tx := storedDepositOrder(models.TxStatusFundsReceived)
	account := storedAccount(models.ProviderBridge)

	h.txRepo.EXPECT().FindLatestByCustomer(userID).Return(tx, nil).Once()
	h.vaRepo.EXPECT().FindByID(*tx.VirtualAccountID).Return(account, nil).Once()
	standing.EXPECT().GetDeposit(testifymock.Anything, testifymock.Anything, account.ExternalAccountID, "dep_1").
		Return(nil, errors.New("bridge: GET /accounts/va_existing/deposits/dep_1: 503")).Once()

	view, err := h.transactions.Latest(context.Background(), userID)
	require.NoError(t, err)
	require.Equal(t, models.TxStatusFundsReceived, view.Status)
	h.broadcaster.AssertNotCalled(t, "Publish", testifymock.Anything, testifymock.Anything)
}

func TestOfframpShowPrefersStoredDepositMode(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	h.config(models.ProviderBridge, true, models.ProviderModeProduction)

	mode := ramps.DepositModeSimulated
	tx := storedOrder(models.TransactionTypeOfframp, models.TxStatusCompleted, "trf_1")
	tx.DepositMode = &mode
	h.txRepo.EXPECT().FindByCustomerAndExternalID(userID, orderID).Return(tx, nil).Once()

	view, err := h.offramp.Show(context.Background(), userID, orderID)
	require.NoError(t, err)
	require.Equal(t, ramps.DepositModeSimulated, view.DepositMode)
}

// A deposit the provider does not report yet is not news: the row is answered
// as stored, and nothing is written or broadcast.
func TestLatestAnswersADepositTheProviderDoesNotReportYetAsStored(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderBridge)
	standing := h.withDepositAdapter(t)
	tx := storedDepositOrder(models.TxStatusFundsReceived)
	account := storedAccount(models.ProviderBridge)

	h.txRepo.EXPECT().FindLatestByCustomer(userID).Return(tx, nil).Once()
	h.vaRepo.EXPECT().FindByID(*tx.VirtualAccountID).Return(account, nil).Once()
	standing.EXPECT().GetDeposit(testifymock.Anything, testifymock.Anything, account.ExternalAccountID, "dep_1").
		Return(nil, nil).Once()

	view, err := h.transactions.Latest(context.Background(), userID)
	require.NoError(t, err)
	require.Equal(t, models.TxStatusFundsReceived, view.Status)
	h.txRepo.AssertNotCalled(t, "SaveProviderSnapshotIfStatus", testifymock.Anything, testifymock.Anything)
	h.broadcaster.AssertNotCalled(t, "Publish", testifymock.Anything, testifymock.Anything)
}

// inFlightOzavOnramp is settledOnramp before the provider has reported the
// pay-in settled: an order-first on-ramp whose crypto will land in Ozav's
// custody.
func inFlightOzavOnramp() *models.Transaction {
	tx := settledOnramp()
	tx.Status = models.TxStatusFundsReceived
	return tx
}

// A read is one of the paths that can see an on-ramp complete first: the app
// polls while the user pays. Its refresh completes the row, and the event that
// lands afterwards finds the order finished — so the read is what must ask for
// the delivery, or the crypto waits in the provider's custody for the next
// reconcile sweep.
func TestShowDeliversAnOnrampItsRefreshCompleted(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderOzav)
	tx := inFlightOzavOnramp()
	h.txRepo.EXPECT().FindByCustomerAndExternalID(userID, tx.ExternalID).Return(tx, nil).Once()
	h.second.EXPECT().GetOrder(testifymock.Anything, testifymock.Anything, tx.GatewayOrderID).
		Return(&ramp.RampOrder{Status: models.TxStatusCompleted}, nil).Once()
	h.txRepo.EXPECT().SaveProviderSnapshotIfStatus(tx, models.TxStatusFundsReceived).Return(true, nil).Once()
	h.delivering.order = &ramp.RampOrder{ProviderOrderID: "ozav_tr_delivery_1"}
	h.txRepo.EXPECT().Update(tx).Return(nil).Once()
	h.broadcaster.EXPECT().Publish(userID, testifymock.Anything).Twice()

	view, err := h.onramp.Show(context.Background(), userID, tx.ExternalID)
	require.NoError(t, err)
	require.Equal(t, models.TxStatusCompleted, view.Status)
	require.NotNil(t, h.delivering.delivered, "the refresh that completed the pay-in asks for the second hop")
	require.Equal(t, "ozav_tr_delivery_1", *tx.DeliveryOrderID)
}

// /transactions/latest re-reads through the same refresh, so it delivers too.
func TestLatestDeliversAnOnrampItsRefreshCompleted(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderOzav)
	tx := inFlightOzavOnramp()
	h.txRepo.EXPECT().FindLatestByCustomer(userID).Return(tx, nil).Once()
	h.second.EXPECT().GetOrder(testifymock.Anything, testifymock.Anything, tx.GatewayOrderID).
		Return(&ramp.RampOrder{Status: models.TxStatusCompleted}, nil).Once()
	h.txRepo.EXPECT().SaveProviderSnapshotIfStatus(tx, models.TxStatusFundsReceived).Return(true, nil).Once()
	h.delivering.order = &ramp.RampOrder{ProviderOrderID: "ozav_tr_delivery_1"}
	h.txRepo.EXPECT().Update(tx).Return(nil).Once()
	h.broadcaster.EXPECT().Publish(userID, testifymock.Anything).Twice()

	_, err := h.transactions.Latest(context.Background(), userID)
	require.NoError(t, err)
	require.Equal(t, "ozav_tr_delivery_1", *tx.DeliveryOrderID)
}

// A delivery that fails does not fail the read: the order did complete, the
// answer is still right, and the event or the sweep retries the send.
func TestShowAnswersTheCompletedOrderWhenItsDeliveryFails(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderOzav)
	tx := inFlightOzavOnramp()
	h.txRepo.EXPECT().FindByCustomerAndExternalID(userID, tx.ExternalID).Return(tx, nil).Once()
	h.second.EXPECT().GetOrder(testifymock.Anything, testifymock.Anything, tx.GatewayOrderID).
		Return(&ramp.RampOrder{Status: models.TxStatusCompleted}, nil).Once()
	h.txRepo.EXPECT().SaveProviderSnapshotIfStatus(tx, models.TxStatusFundsReceived).Return(true, nil).Once()
	h.delivering.err = errors.New("ozav: POST /transfers: 503")
	h.broadcaster.EXPECT().Publish(userID, testifymock.Anything).Once()

	view, err := h.onramp.Show(context.Background(), userID, tx.ExternalID)
	require.NoError(t, err)
	require.Equal(t, models.TxStatusCompleted, view.Status)
	require.Nil(t, tx.DeliveryOrderID)
}

// When another writer — the event, a second read — completed the row between
// this read's provider call and its write, the refresh is not the one that saw
// the order complete, and the winner is the one that asks.
func TestShowLeavesTheDeliveryToTheWriterThatWonTheRefresh(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderOzav)
	tx := inFlightOzavOnramp()
	won := settledOnramp()
	won.Status = models.TxStatusProcessing
	won.DeliveryOrderID = support.Ptr("ozav_tr_delivery_1")
	h.txRepo.EXPECT().FindByCustomerAndExternalID(userID, tx.ExternalID).Return(tx, nil).Once()
	h.second.EXPECT().GetOrder(testifymock.Anything, testifymock.Anything, tx.GatewayOrderID).
		Return(&ramp.RampOrder{Status: models.TxStatusCompleted}, nil).Once()
	h.txRepo.EXPECT().SaveProviderSnapshotIfStatus(tx, models.TxStatusFundsReceived).Return(false, nil).Once()
	h.txRepo.EXPECT().FindByCustomerAndExternalID(userID, tx.ExternalID).Return(won, nil).Twice()
	h.second.EXPECT().GetOrder(testifymock.Anything, testifymock.Anything, "ozav_tr_delivery_1").
		Return(&ramp.RampOrder{Status: models.TxStatusCompleted, TxHash: "5sig"}, nil).Once()
	h.txRepo.EXPECT().SaveDeliveryOutcome(testifymock.Anything, "ozav_tr_delivery_1").
		Return(false, nil).Once()

	_, err := h.onramp.Show(context.Background(), userID, tx.ExternalID)
	require.NoError(t, err)
	require.Nil(t, h.delivering.delivered)
}

// The delivery transfer is read for its outcome only: its id and its token
// amount belong to another movement, and a failure has to make the order due
// again rather than read as "processing" forever.
func TestShowRecordsAFailedDeliveryWithoutTouchingThePayIn(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderOzav)
	tx := settledOnramp()
	tx.Status = models.TxStatusProcessing
	tx.DeliveryOrderID = support.Ptr("ozav_tr_delivery_1")
	payIn, amountFiat := tx.GatewayOrderID, tx.AmountFiat
	h.txRepo.EXPECT().FindByCustomerAndExternalID(userID, tx.ExternalID).Return(tx, nil).Once()
	h.second.EXPECT().GetOrder(testifymock.Anything, testifymock.Anything, payIn).
		Return(&ramp.RampOrder{ProviderOrderID: payIn, Status: tx.Status}, nil).Maybe()
	h.second.EXPECT().GetOrder(testifymock.Anything, testifymock.Anything, "ozav_tr_delivery_1").
		Return(&ramp.RampOrder{
			ProviderOrderID: "ozav_tr_delivery_1", Status: models.TxStatusProcessing,
			DeliveryFailed: true, FailureReason: "boom",
			Amounts: map[string]string{"source_amount": "88.2"},
		}, nil).Once()
	h.txRepo.EXPECT().SaveDeliveryOutcome(tx, "ozav_tr_delivery_1").Return(true, nil).Once()
	// The failure makes the order due again, and the read that saw it asks.
	h.delivering.order = &ramp.RampOrder{ProviderOrderID: "ozav_tr_delivery_2"}
	h.txRepo.EXPECT().Update(tx).Return(nil).Once()
	h.broadcaster.EXPECT().Publish(userID, testifymock.Anything).Twice()

	_, err := h.onramp.Show(context.Background(), userID, tx.ExternalID)
	require.NoError(t, err)
	require.Equal(t, payIn, tx.GatewayOrderID)
	require.Equal(t, amountFiat, tx.AmountFiat)
	require.Equal(t, 1, tx.DeliveryAttempts)
	require.NotNil(t, tx.FailureReason)
	require.Contains(t, *tx.FailureReason, "boom")
	require.NotNil(t, h.delivering.delivered)
	require.Equal(t, "onramp-delivery:"+tx.ExternalID+":1", h.delivering.delivered.IdempotencyKey)
	require.Equal(t, "ozav_tr_delivery_2", *tx.DeliveryOrderID)
}

// The ceiling holds on every path that can place a send, not only the sweep
// that filters on it.
func TestDeliverRefusesAnOnrampPastItsAttemptCeiling(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderOzav)
	tx := settledOnramp()
	tx.DeliveryAttempts = ramp.DefaultOzavDeliveryMaxAttempts

	require.NoError(t, h.onramp.Deliver(context.Background(), tx))
	require.Nil(t, h.delivering.delivered)
}

// A bank payout is owned by the webhook, the reconciler and the recovery, which
// read it against its stored binding. The client read goes through the generic
// GetOrder, which is unbound and unclamped, so it leaves a bank order as stored
// and never calls the provider.
func TestShowNeverRefreshesABankPayoutThroughTheGenericRead(t *testing.T) {
	for _, status := range []string{models.TxStatusRefundAttention, models.TxStatusProcessing} {
		for _, gatewayID := range []string{"po_1", "trf_1"} {
			h := newHarness(t, models.RailACHPayout, models.ProviderBridge)
			tx := storedOrder(models.TransactionTypeOfframp, status, gatewayID)
			rail := models.RailACHPayout
			tx.Rail = &rail

			h.txRepo.EXPECT().FindByCustomerAndExternalID(userID, orderID).Return(tx, nil).Once()
			// Were it asked, the provider would move the order.
			h.adapter.EXPECT().GetOrder(testifymock.Anything, testifymock.Anything, gatewayID).
				Return(&ramp.RampOrder{Status: models.TxStatusCompleted}, nil).Maybe()
			h.txRepo.EXPECT().SaveProviderSnapshotIfStatus(testifymock.Anything, testifymock.Anything).Return(true, nil).Maybe()
			h.broadcaster.EXPECT().Publish(testifymock.Anything, testifymock.Anything).Maybe()

			view, err := h.offramp.Show(context.Background(), userID, orderID)
			require.NoError(t, err)
			require.Equal(t, status, view.Status, "%s/%s is left as stored", status, gatewayID)
			require.Equal(t, status, tx.Status)
			h.adapter.AssertNotCalled(t, "GetOrder",
				testifymock.Anything, testifymock.Anything, testifymock.Anything)
			h.txRepo.AssertNotCalled(t, "SaveProviderSnapshotIfStatus", testifymock.Anything, testifymock.Anything)
			require.Empty(t, h.announced)
		}
	}
}
