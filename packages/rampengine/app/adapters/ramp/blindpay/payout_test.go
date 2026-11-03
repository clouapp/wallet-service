package blindpay_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp/blindpay"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

const (
	payoutID    = "po_000000000001"
	bankAcctID  = "ba_000000000001"
	payoutQuote = "qu_000000000001"
)

// payoutBody is a valid PayoutOut for the 100.00 USD quote, with overrides; a
// nil override removes the field.
func payoutBody(t *testing.T, overrides map[string]any) string {
	t.Helper()
	body := map[string]any{
		"id":                    payoutID,
		"status":                "processing",
		"customer_id":           customerID,
		"quote_id":              payoutQuote,
		"bank_account_id":       bankAcctID,
		"network":               "solana_devnet",
		"token":                 "USDB",
		"sender_amount":         10000,
		"receiver_amount":       9850,
		"sender_wallet_address": walletAddr,
		"tracking_transaction":  map[string]any{"step": "processing"},
		"tracking_payment":      map[string]any{"step": "processing"},
		"tracking_complete":     map[string]any{"step": "processing"},
	}
	for k, v := range overrides {
		if v == nil {
			delete(body, k)
			continue
		}
		body[k] = v
	}
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	return string(encoded)
}

func payoutBinding() ramp.OrderBinding {
	return ramp.OrderBinding{
		ProviderCustomerID: customerID,
		BankAccountID:      bankAcctID,
		QuoteID:            payoutQuote,
		Network:            "solana_devnet",
		Token:              "USDB",
		SenderAmount:       "100.00",
		SenderWallet:       walletAddr,
	}
}

func TestGetBoundOrder_ReadsAPayoutThatMatchesItsBinding(t *testing.T) {
	read := payoutBody(t, map[string]any{
		"status":               "completed",
		"tracking_transaction": map[string]any{"step": "completed", "status": "found", "transaction_hash": "5collect"},
		"tracking_complete":    map[string]any{"step": "completed", "status": "paid", "transaction_hash": "5refundsig"},
	})
	adapter, _, calls := payinServer(t, models.ProviderModeSandbox, map[string]stubAnswer{
		"GET payouts/" + payoutID: {200, read},
	})

	order, err := adapter.GetBoundOrder(context.Background(), payinRef, payoutID, payoutBinding())
	require.NoError(t, err)

	got := calls()
	require.Len(t, got, 1)
	assert.Equal(t, "GET "+instancePrefix+"payouts/"+payoutID, got[0].method+" "+got[0].path)
	assert.Equal(t, payoutID, order.ProviderOrderID)
	assert.Equal(t, models.TxStatusCompleted, order.Status)
	assert.Equal(t, "5collect", order.TxHash, "the collection hash, not the refund's")
	assert.Equal(t, map[string]string{"source_amount": "100.00", "destination_amount": "98.50"}, order.Amounts)
	assert.JSONEq(t, read, string(order.Raw))
}

func TestGetBoundOrder_TheFixtureIsReadAsAPayout(t *testing.T) {
	read, err := os.ReadFile("testdata/payout_completed.json")
	require.NoError(t, err)
	adapter, _, _ := payinServer(t, models.ProviderModeSandbox, map[string]stubAnswer{
		"GET payouts/" + payoutID: {200, string(read)},
	})
	order, err := adapter.GetBoundOrder(context.Background(), payinRef, payoutID, payoutBinding())
	require.NoError(t, err)
	assert.Equal(t, models.TxStatusCompleted, order.Status)
	assert.Equal(t, "5collectsig", order.TxHash)
}

// Each field of the binding is checked on its own: one that differs refuses
// the payout and nothing of it is applied.
func TestGetBoundOrder_RefusesEachDivergentField(t *testing.T) {
	cases := map[string]map[string]any{
		"customer":       {"customer_id": "re_000000000002"},
		"quote":          {"quote_id": "qu_000000000002"},
		"bank account":   {"bank_account_id": "ba_000000000002"},
		"network":        {"network": "solana"},
		"token":          {"token": "USDC"},
		"sender amount":  {"sender_amount": 10001},
		"sender wallet":  {"sender_wallet_address": "7xKXtg2CW87d97TXJSDpbD5jBkheTqA83TZRuJosgAsU"},
		"id":             {"id": "po_000000000002"},
		"missing amount": {"sender_amount": nil},
		"missing quote":  {"quote_id": nil},
	}
	for name, overrides := range cases {
		t.Run(name, func(t *testing.T) {
			logger := captureLog(t)
			logger.EXPECT().Errorf("ramp.attention: blindpay payout %s does not match its binding", payoutID).Once()
			overrides["status"] = "completed"
			adapter, _, _ := payinServer(t, models.ProviderModeSandbox, map[string]stubAnswer{
				"GET payouts/" + payoutID: {200, payoutBody(t, overrides)},
			})

			order, err := adapter.GetBoundOrder(context.Background(), payinRef, payoutID, payoutBinding())
			assert.Nil(t, order)
			require.ErrorIs(t, err, ramp.ErrBindingMismatch)
			assert.NotContains(t, err.Error(), walletAddr)
			logger.AssertExpectations(t)
		})
	}
}

// A binding with a field left empty would match a payout that omits it, so it
// is refused outright.
func TestGetBoundOrder_RefusesAnIncompleteBinding(t *testing.T) {
	blank := map[string]func(*ramp.OrderBinding){
		"customer":      func(b *ramp.OrderBinding) { b.ProviderCustomerID = "" },
		"quote":         func(b *ramp.OrderBinding) { b.QuoteID = "" },
		"bank account":  func(b *ramp.OrderBinding) { b.BankAccountID = "" },
		"network":       func(b *ramp.OrderBinding) { b.Network = "" },
		"token":         func(b *ramp.OrderBinding) { b.Token = "" },
		"sender amount": func(b *ramp.OrderBinding) { b.SenderAmount = "" },
		"sender wallet": func(b *ramp.OrderBinding) { b.SenderWallet = "" },
	}
	for name, mutate := range blank {
		t.Run(name, func(t *testing.T) {
			logger := captureLog(t)
			logger.EXPECT().Errorf("ramp.attention: blindpay payout %s does not match its binding", payoutID).Once()
			adapter, _, _ := payinServer(t, models.ProviderModeSandbox, map[string]stubAnswer{
				"GET payouts/" + payoutID: {200, payoutBody(t, nil)},
			})
			binding := payoutBinding()
			mutate(&binding)
			order, err := adapter.GetBoundOrder(context.Background(), payinRef, payoutID, binding)
			assert.Nil(t, order)
			require.ErrorIs(t, err, ramp.ErrBindingMismatch)
		})
	}
}

// The binding spells the amount as the quote did; the payout answers cents.
func TestGetBoundOrder_ComparesTheAmountByValue(t *testing.T) {
	adapter, _, _ := payinServer(t, models.ProviderModeSandbox, map[string]stubAnswer{
		"GET payouts/" + payoutID: {200, payoutBody(t, nil)},
	})
	for _, spelling := range []string{"100", "100.00", "100.0"} {
		binding := payoutBinding()
		binding.SenderAmount = spelling
		_, err := adapter.GetBoundOrder(context.Background(), payinRef, payoutID, binding)
		require.NoError(t, err, spelling)
	}
}

func TestGetBoundOrder_RefusesLocallyWithoutCalling(t *testing.T) {
	cases := map[string]struct {
		ref  ramp.CustomerRef
		id   string
		want error
	}{
		"no customer":  {ramp.CustomerRef{}, payoutID, ramp.ErrIntegrationRequired},
		"a payin id":   {payinRef, payinID, ramp.ErrBindingMismatch},
		"no id":        {payinRef, "", ramp.ErrBindingMismatch},
		"path escape":  {payinRef, "po_/../customers", ramp.ErrBindingMismatch},
		"short payout": {payinRef, "po_1", ramp.ErrBindingMismatch},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			adapter, _, calls := payinServer(t, models.ProviderModeSandbox, nil)
			_, err := adapter.GetBoundOrder(context.Background(), tc.ref, tc.id, payoutBinding())
			assert.ErrorIs(t, err, tc.want)
			assert.Empty(t, calls())
		})
	}
}

func TestGetBoundOrder_UnreadableAnswerFails(t *testing.T) {
	adapter, _, _ := payinServer(t, models.ProviderModeSandbox, map[string]stubAnswer{
		"GET payouts/" + payoutID: {200, `{"id":`},
	})
	_, err := adapter.GetBoundOrder(context.Background(), payinRef, payoutID, payoutBinding())
	require.ErrorIs(t, err, ramp.ErrProviderFailed)
}

func TestGetBoundOrder_ProviderErrorPassesThrough(t *testing.T) {
	adapter, _, _ := payinServer(t, models.ProviderModeSandbox, map[string]stubAnswer{
		"GET payouts/" + payoutID: {500, `{"code":"boom"}`},
	})
	order, err := adapter.GetBoundOrder(context.Background(), payinRef, payoutID, payoutBinding())
	assert.Nil(t, order)
	require.Error(t, err)
	assert.NotErrorIs(t, err, ramp.ErrBindingMismatch)
}

// A refund_attention payout is flagged on every read, by id only.
func TestGetBoundOrder_RefundAttentionIsLogged(t *testing.T) {
	logger := captureLog(t)
	logger.EXPECT().Errorf("ramp.attention: blindpay payout %s %s", payoutID, models.TxStatusRefundAttention).Once()
	adapter, _, _ := payinServer(t, models.ProviderModeSandbox, map[string]stubAnswer{
		"GET payouts/" + payoutID: {200, payoutBody(t, map[string]any{
			"status":               "failed",
			"tracking_transaction": map[string]any{"step": "completed", "status": "found", "transaction_hash": "5collect"},
		})},
	})
	order, err := adapter.GetBoundOrder(context.Background(), payinRef, payoutID, payoutBinding())
	require.NoError(t, err)
	assert.Equal(t, models.TxStatusRefundAttention, order.Status)
	logger.AssertExpectations(t)
}

func TestGetBoundOrder_AnUnknownStatusIsProcessingAndLogged(t *testing.T) {
	logger := captureLog(t)
	logger.EXPECT().Errorf("ramp.attention: blindpay payout %s has a status it does not know", payoutID).Once()
	adapter, _, _ := payinServer(t, models.ProviderModeSandbox, map[string]stubAnswer{
		"GET payouts/" + payoutID: {200, payoutBody(t, map[string]any{"status": "mystery"})},
	})
	order, err := adapter.GetBoundOrder(context.Background(), payinRef, payoutID, payoutBinding())
	require.NoError(t, err)
	assert.Equal(t, models.TxStatusProcessing, order.Status)
	logger.AssertExpectations(t)
}

func TestGetBoundOrder_FailureReasonIsCarried(t *testing.T) {
	adapter, _, _ := payinServer(t, models.ProviderModeSandbox, map[string]stubAnswer{
		"GET payouts/" + payoutID: {200, payoutBody(t, map[string]any{
			"status":               "failed",
			"tracking_transaction": map[string]any{"step": "completed", "status": "failed", "provider_error_reason": "erc20_allowance_insufficient"},
		})},
	})
	order, err := adapter.GetBoundOrder(context.Background(), payinRef, payoutID, payoutBinding())
	require.NoError(t, err)
	assert.Equal(t, models.TxStatusFailed, order.Status)
	assert.Equal(t, "erc20_allowance_insufficient", order.FailureReason)
}

// A payout is read only bound to its quote: the generic read refuses it
// without calling, while a payin id keeps its path (the suite above).
func TestGetOrder_APayoutIsOnlyReadBound(t *testing.T) {
	adapter, _, calls := payinServer(t, models.ProviderModeSandbox, map[string]stubAnswer{
		"GET payouts/" + payoutID: {200, payoutBody(t, map[string]any{"status": "completed"})},
	})
	order, err := adapter.GetOrder(context.Background(), payinRef, payoutID)
	assert.Nil(t, order)
	require.ErrorIs(t, err, ramp.ErrBindingMismatch)
	assert.EqualError(t, err, "blindpay: a payout is read bound to its quote: "+ramp.ErrBindingMismatch.Error())
	assert.Empty(t, calls())
}

func TestPayoutAdapterImplementsTheBoundReader(t *testing.T) {
	var _ ramp.BoundOrderReader = (*blindpay.Adapter)(nil)
}

// The ingest path end to end: a signed payout.* delivery decodes to a ramp
// event with the mapped status and the collection hash.
func TestParseWebhook_SignedPayoutEventsAreRampEvents(t *testing.T) {
	for _, event := range []string{"payout.new", "payout.update", "payout.complete"} {
		t.Run(event, func(t *testing.T) {
			body := payoutEvent(t, event, map[string]any{
				"status":               "completed",
				"tracking_transaction": map[string]any{"step": "completed", "status": "found", "transaction_hash": "5collect"},
				"tracking_complete":    map[string]any{"step": "completed", "transaction_hash": "5refund"},
			})
			adapter := blindpay.New(nil, secretFactory(testSecret, nil), nil)
			result, err := adapter.ParseWebhook(context.Background(),
				signedHeaders(testSecret, "msg_po", time.Now(), string(body)), body)
			require.NoError(t, err)
			assert.Equal(t, "msg_po", result.EventID)
			assert.Equal(t, event, result.EventType)
			assert.Equal(t, ramp.WebhookKindRamp, result.Kind)
			assert.Equal(t, payoutID, result.ProviderOrderID)
			assert.Equal(t, models.TxStatusCompleted, result.Status)
			assert.Equal(t, "5collect", result.TxHash)
			assert.Equal(t, "100.00", result.SourceAmount)
			assert.Equal(t, "98.50", result.DestinationAmount)
			assert.JSONEq(t, string(body), string(result.Raw))
		})
	}
}

func TestContract_PayoutResponse(t *testing.T) {
	requireFieldsInSchema(t, "GET", "/v1/instances/{instance_id}/payouts/{id}", 200, blindpay.PayoutResponse{})
}
