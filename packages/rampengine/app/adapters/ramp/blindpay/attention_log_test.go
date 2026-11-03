package blindpay_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp/blindpay"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/tests/support"
)

// The attention paths of the adapter each write a line starting with
// ramp.attention:, the marker the operator alerts match on. These tests pin the
// prefix and that no amount or address appears; the text after the marker is
// free to change.

func attentionLines(t *testing.T) *support.ErrorLines {
	t.Helper()
	return support.CaptureErrorLines(captureLog(t))
}

func TestAttentionMarker_PayoutInRefundAttentionApplied(t *testing.T) {
	lines := attentionLines(t)
	result, err := blindpay.New(nil, nil, nil).DecodeWebhook(context.Background(), payoutEvent(t, "payout.update",
		map[string]any{"status": "processing", "tracking_payment": map[string]any{"step": "completed", "provider_status": "returned"}}))
	require.NoError(t, err)
	require.Equal(t, models.TxStatusRefundAttention, result.Status)
	lines.RequireAttention(t, walletAddr, "10000", "100.00")
}

func TestAttentionMarker_GetBoundOrderMismatch(t *testing.T) {
	lines := attentionLines(t)
	adapter, _, _ := payinServer(t, models.ProviderModeSandbox, map[string]stubAnswer{
		"GET payouts/" + payoutID: {200, payoutBody(t, map[string]any{"status": "completed", "sender_amount": 10001})},
	})
	_, err := adapter.GetBoundOrder(context.Background(), payinRef, payoutID, payoutBinding())
	require.ErrorIs(t, err, ramp.ErrBindingMismatch)
	lines.RequireAttention(t, walletAddr, "10001", "10000")
}

func TestAttentionMarker_PrepareRefusedByPolicy(t *testing.T) {
	lines := attentionLines(t)
	spec := validDelegation()
	spec.amount = 10_120_001
	adapter, _ := prepareServer(t, stubAnswer{200, prepareAnswer(t, spec.build(t))}, nil)
	_, err := adapter.PrepareOfframp(context.Background(), payinRef, prepareRequest())
	require.ErrorIs(t, err, ramp.ErrProviderFailed)
	lines.RequireAttention(t, prepareOwner.PublicKey().String(), sandboxMint, sandboxDelegate, "10120000", "10120001", "10.12")
}
