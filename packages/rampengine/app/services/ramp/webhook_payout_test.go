package ramp_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/goravel/framework/database/orm"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	rampsvc "github.com/macrowallets/waas/packages/rampengine/app/services/ramp"
)

type fakeBindings struct {
	binding *ramp.OrderBinding
	err     error
	asked   []uint
}

func (f *fakeBindings) PayoutBinding(txID uint) (*ramp.OrderBinding, error) {
	f.asked = append(f.asked, txID)
	return f.binding, f.err
}

func payoutBinding() *ramp.OrderBinding {
	return &ramp.OrderBinding{
		ProviderCustomerID: "re_1", BankAccountID: "ba_1", QuoteID: "qu_1", Network: "solana", Token: "USDC",
		SenderAmount: "10", SenderWallet: "Wallet1",
	}
}

func bankPayout(status string) *models.Transaction {
	rail := models.RailACHPayout
	return &models.Transaction{
		Model: orm.Model{ID: 1}, CustomerID: 7, UserID: 7, Provider: models.ProviderBridge,
		Type: models.TransactionTypeOfframp, ExternalID: "ext_1", GatewayOrderID: "po_1", Rail: &rail,
		Status: status, AmountFiat: "0", AmountToken: "10", TargetToken: "usdc",
	}
}

func payoutEvent(status string) *ramp.WebhookResult {
	return &ramp.WebhookResult{
		Kind: ramp.WebhookKindRamp, EventID: "evt_1", EventType: "payout.update", ProviderOrderID: "po_1",
		Status: status, ProviderCustomerID: "re_1", BankAccountID: "ba_1", SenderWallet: "Wallet1",
	}
}

// recordingMarker is the attention mark: the codes it was asked to write, and
// the error it answers.
type recordingMarker struct {
	codes []string
	ids   []uint
	err   error
}

func (m *recordingMarker) MarkAttention(txID uint, code string, _ time.Time) (bool, error) {
	m.ids, m.codes = append(m.ids, txID), append(m.codes, code)
	return m.err == nil, m.err
}

func (h *harness) withBindingsAndMarker(b rampsvc.BindingSource, m rampsvc.AttentionMarker) {
	provider := &standingAdapter{MockProviderAdapter: h.adapter, MockVirtualAccountAdapter: h.standing}
	h.processor = rampsvc.NewWebhookProcessor(rampsvc.WebhookProcessorDeps{
		Factory: ramp.NewFactory(provider), Events: h.events, Integrations: h.integRepo, Accounts: h.accounts,
		Members: h.members, Broadcaster: h.broadcaster, Notifier: h.notifier, Features: h.features,
		Deliverer: h.deliverer, Bindings: b, Attention: m,
	})
}

func (h *harness) withBindings(b rampsvc.BindingSource) {
	provider := &standingAdapter{MockProviderAdapter: h.adapter, MockVirtualAccountAdapter: h.standing}
	h.processor = rampsvc.NewWebhookProcessor(rampsvc.WebhookProcessorDeps{
		Factory: ramp.NewFactory(provider), Events: h.events, Integrations: h.integRepo, Accounts: h.accounts,
		Members: h.members, Broadcaster: h.broadcaster, Notifier: h.notifier, Features: h.features,
		Deliverer: h.deliverer, Bindings: b,
	})
}

// Review Focus 4: an old payout.update arriving after the refund review began.
func TestProcessKeepsAPayoutUnderRefundReviewOutOfProcessing(t *testing.T) {
	for _, current := range []string{models.TxStatusRefundAttention, models.TxStatusReturning} {
		t.Run(current, func(t *testing.T) {
			h := newHarness(t)
			h.withBindings(&fakeBindings{binding: payoutBinding()})
			row := eventRow()
			tx := bankPayout(current)

			h.storedEvent(row)
			h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(payoutEvent(models.TxStatusProcessing), nil).Once()
			h.db.EXPECT().FindOrderForUpdate(models.ProviderBridge, "po_1").Return(tx, nil).Once()
			h.db.EXPECT().UpdateTransaction(tx).Return(nil).Maybe()
			h.broadcaster.EXPECT().Publish(testifymock.Anything, testifymock.Anything).Maybe()
			h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

			require.NoError(t, h.processor.Process(context.Background(), eventRowID))
			require.Equal(t, current, tx.Status)
		})
	}
}

// Held funds are never hidden by a terminal failed: the status stays (the
// event row keeps the raw payload).
func TestProcessKeepsAPayoutUnderRefundReviewOutOfFailed(t *testing.T) {
	for _, current := range []string{models.TxStatusRefundAttention, models.TxStatusReturning} {
		t.Run(current, func(t *testing.T) {
			h := newHarness(t)
			h.withBindings(&fakeBindings{binding: payoutBinding()})
			row := eventRow()
			tx := bankPayout(current)

			h.storedEvent(row)
			h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(payoutEvent(models.TxStatusFailed), nil).Once()
			h.db.EXPECT().FindOrderForUpdate(models.ProviderBridge, "po_1").Return(tx, nil).Once()
			h.db.EXPECT().UpdateTransaction(tx).Return(nil).Once()
			h.broadcaster.EXPECT().Publish(testifymock.Anything, testifymock.Anything).Maybe()
			h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

			require.NoError(t, h.processor.Process(context.Background(), eventRowID))
			require.Equal(t, current, tx.Status)
		})
	}
}

func TestProcessMovesAPayoutUnderRefundReviewForward(t *testing.T) {
	h := newHarness(t)
	h.withBindings(&fakeBindings{binding: payoutBinding()})
	row := eventRow()
	tx := bankPayout(models.TxStatusRefundAttention)

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(payoutEvent(models.TxStatusRefunded), nil).Once()
	h.db.EXPECT().FindOrderForUpdate(models.ProviderBridge, "po_1").Return(tx, nil).Once()
	h.db.EXPECT().UpdateTransaction(tx).Return(nil).Once()
	h.broadcaster.EXPECT().Publish(testifymock.Anything, testifymock.Anything).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.Equal(t, models.TxStatusRefunded, tx.Status)
}

func TestProcessAllowsACompletedPayoutToBeRefunded(t *testing.T) {
	h := newHarness(t)
	h.withBindings(&fakeBindings{binding: payoutBinding()})
	row := eventRow()
	tx := bankPayout(models.TxStatusCompleted)

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(payoutEvent(models.TxStatusRefunded), nil).Once()
	h.db.EXPECT().FindOrderForUpdate(models.ProviderBridge, "po_1").Return(tx, nil).Once()
	h.db.EXPECT().UpdateTransaction(tx).Return(nil).Once()
	h.broadcaster.EXPECT().Publish(testifymock.Anything, testifymock.Anything).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.Equal(t, models.TxStatusRefunded, tx.Status)
}

func TestProcessAppliesAPayoutEventThatMatchesTheStoredBinding(t *testing.T) {
	h := newHarness(t)
	h.withBindings(&fakeBindings{binding: payoutBinding()})
	row := eventRow()
	tx := bankPayout(models.TxStatusProcessing)
	event := payoutEvent(models.TxStatusCompleted)
	event.DestinationAmount, event.SourceAmount = "98.5", "10"

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(event, nil).Once()
	h.db.EXPECT().FindOrderForUpdate(models.ProviderBridge, "po_1").Return(tx, nil).Once()
	h.db.EXPECT().UpdateTransaction(tx).Return(nil).Once()
	h.broadcaster.EXPECT().Publish(testifymock.Anything, testifymock.Anything).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.Equal(t, models.TxStatusCompleted, tx.Status)
	require.Equal(t, "98.5", tx.AmountFiat)
}

// A prepared Pix payout's event is held to the stored binding too, and what
// it settled lands in amount_brl, never in the dollar leg.
func TestProcessAppliesAPreparedPixPayoutEventThatMatchesTheBinding(t *testing.T) {
	h := newHarness(t)
	h.withBindings(&fakeBindings{binding: payoutBinding()})
	row := eventRow()
	tx := bankPayout(models.TxStatusProcessing)
	pix, prepared := models.RailPixPayout, models.FundingModePrepared
	tx.Rail, tx.FundingMode = &pix, &prepared
	event := payoutEvent(models.TxStatusCompleted)
	event.DestinationAmount, event.SourceAmount = "54.20", "10"

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(event, nil).Once()
	h.db.EXPECT().FindOrderForUpdate(models.ProviderBridge, "po_1").Return(tx, nil).Once()
	h.db.EXPECT().UpdateTransaction(tx).Return(nil).Once()
	h.broadcaster.EXPECT().Publish(testifymock.Anything, testifymock.Anything).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.Equal(t, models.TxStatusCompleted, tx.Status)
	require.Equal(t, "54.20", tx.AmountFiat)
}

// A Pix order of a deposit provider is not held to a payout binding: it has
// none, and its event applies by the provider order id as before.
func TestProcessAppliesADepositPixEventWithoutAPayoutBinding(t *testing.T) {
	h := newHarness(t)
	h.withBindings(&fakeBindings{})
	row := eventRow()
	tx := bankPayout(models.TxStatusProcessing)
	pix := models.RailPixPayout
	tx.Rail = &pix
	event := payoutEvent(models.TxStatusCompleted)
	event.DestinationAmount, event.SourceAmount = "54.20", "10"

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(event, nil).Once()
	h.db.EXPECT().FindOrderForUpdate(models.ProviderBridge, "po_1").Return(tx, nil).Once()
	h.db.EXPECT().UpdateTransaction(tx).Return(nil).Once()
	h.broadcaster.EXPECT().Publish(testifymock.Anything, testifymock.Anything).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.Equal(t, models.TxStatusCompleted, tx.Status)
}

// The ruling: no binding match, no status. Every field the payload carries is
// held, and a missing stored binding is a mismatch for a bank order.
func TestProcessAppliesNothingWhenThePayoutEventDoesNotMatchTheBinding(t *testing.T) {
	cases := map[string]struct {
		bindings rampsvc.BindingSource
		mutate   func(*ramp.WebhookResult)
	}{
		"other customer":     {&fakeBindings{binding: payoutBinding()}, func(r *ramp.WebhookResult) { r.ProviderCustomerID = "re_2" }},
		"other bank account": {&fakeBindings{binding: payoutBinding()}, func(r *ramp.WebhookResult) { r.BankAccountID = "ba_2" }},
		"other wallet":       {&fakeBindings{binding: payoutBinding()}, func(r *ramp.WebhookResult) { r.SenderWallet = "Other" }},
		"omits customer":     {&fakeBindings{binding: payoutBinding()}, func(r *ramp.WebhookResult) { r.ProviderCustomerID = "" }},
		"omits wallet":       {&fakeBindings{binding: payoutBinding()}, func(r *ramp.WebhookResult) { r.SenderWallet = "" }},
		"no stored binding":  {&fakeBindings{}, func(*ramp.WebhookResult) {}},
		"no binding source":  {nil, func(*ramp.WebhookResult) {}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.withBindings(tc.bindings)
			row := eventRow()
			tx := bankPayout(models.TxStatusProcessing)
			event := payoutEvent(models.TxStatusCompleted)
			tc.mutate(event)

			h.storedEvent(row)
			h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(event, nil).Once()
			h.db.EXPECT().FindOrderForUpdate(models.ProviderBridge, "po_1").Return(tx, nil).Once()
			h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

			require.NoError(t, h.processor.Process(context.Background(), eventRowID))
			require.Equal(t, models.TxStatusProcessing, tx.Status)
			h.db.AssertNotCalled(t, "UpdateTransaction", testifymock.Anything)
			h.broadcaster.AssertNotCalled(t, "Publish", testifymock.Anything, testifymock.Anything)
		})
	}
}

// BlindPay's real payout events do not name the bank account (S10): the event
// is applied on the customer and the wallet it does name, and one that names a
// different bank account is still refused (the table above).
func TestProcessAppliesAPayoutEventThatDoesNotNameTheBankAccount(t *testing.T) {
	h := newHarness(t)
	h.withBindings(&fakeBindings{binding: payoutBinding()})
	row := eventRow()
	tx := bankPayout(models.TxStatusProcessing)
	event := payoutEvent(models.TxStatusCompleted)
	event.BankAccountID = ""

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(event, nil).Once()
	h.db.EXPECT().FindOrderForUpdate(models.ProviderBridge, "po_1").Return(tx, nil).Once()
	h.db.EXPECT().UpdateTransaction(tx).Return(nil).Once()
	h.broadcaster.EXPECT().Publish(testifymock.Anything, testifymock.Anything).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.Equal(t, models.TxStatusCompleted, tx.Status)
}

// An unreadable binding is a failure to retry, never a pass.
func TestProcessFailsWhenTheStoredBindingCannotBeRead(t *testing.T) {
	h := newHarness(t)
	h.withBindings(&fakeBindings{err: errors.New("db down")})
	row := eventRow()
	tx := bankPayout(models.TxStatusProcessing)

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(payoutEvent(models.TxStatusCompleted), nil).Once()
	h.db.EXPECT().FindOrderForUpdate(models.ProviderBridge, "po_1").Return(tx, nil).Once()
	h.db.EXPECT().UpdateEvent(testifymock.Anything).Return(nil).Once()

	require.Error(t, h.processor.Process(context.Background(), eventRowID))
	require.Equal(t, models.TxStatusProcessing, tx.Status)
	h.db.AssertNotCalled(t, "UpdateTransaction", testifymock.Anything)
}

// A binding mismatch applies nothing and raises the attention mark, after the
// event's commit. A marking error is logged and the event still stamps clean:
// a retry would only read the same mismatch.
func TestProcessMarksAPayoutEventThatDoesNotMatchTheBinding(t *testing.T) {
	for name, markErr := range map[string]error{"marked": nil, "mark fails": errors.New("database down")} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			marker := &recordingMarker{err: markErr}
			h.withBindingsAndMarker(&fakeBindings{binding: payoutBinding()}, marker)
			row := eventRow()
			tx := bankPayout(models.TxStatusProcessing)
			event := payoutEvent(models.TxStatusCompleted)
			event.BankAccountID = "ba_other"

			h.storedEvent(row)
			h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(event, nil).Once()
			h.db.EXPECT().FindOrderForUpdate(models.ProviderBridge, "po_1").Return(tx, nil).Once()
			h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

			require.NoError(t, h.processor.Process(context.Background(), eventRowID))
			require.Equal(t, models.TxStatusProcessing, tx.Status)
			require.Equal(t, []string{models.AttentionBindingMismatch}, marker.codes)
			require.Equal(t, []uint{tx.ID}, marker.ids)
			h.db.AssertNotCalled(t, "UpdateTransaction", testifymock.Anything)
		})
	}
}

// A missing binding is logged, not marked; a matching event marks nothing.
func TestProcessDoesNotMarkAMissingBindingOrAMatchingEvent(t *testing.T) {
	h := newHarness(t)
	marker := &recordingMarker{}
	h.withBindingsAndMarker(&fakeBindings{}, marker)
	row := eventRow()
	tx := bankPayout(models.TxStatusProcessing)

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(payoutEvent(models.TxStatusCompleted), nil).Once()
	h.db.EXPECT().FindOrderForUpdate(models.ProviderBridge, "po_1").Return(tx, nil).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.Empty(t, marker.codes)
}
