package blindpay

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func step(s string) payinTracking { return payinTracking{Step: s} }

func payin(status, payment string) payinResponse {
	return payinResponse{Status: status, TrackingPayment: step(payment)}
}

func TestPayinStatus(t *testing.T) {
	refundReview := payin("processing", "completed")
	refundReview.TrackingComplete = step("pending_refund_review")
	txnReview := payin("on_hold", "processing")
	txnReview.TrackingTransaction = step("pending_refund_review")

	staleRefunded := payin("refunded", "completed")
	staleRefunded.TrackingComplete = step("pending_refund_review")
	staleFailedPaid := payin("failed", "completed")
	staleFailedPaid.TrackingTransaction = step("pending_refund_review")
	paymentReview := payin("processing", "pending_refund_review")

	cases := []struct {
		name      string
		in        payinResponse
		want      string
		attention bool
	}{
		{"completed", payin("completed", "completed"), "completed", false},
		{"refunded", payin("refunded", "completed"), "refunded", false},
		{"failed and paid", payin("failed", "completed"), "refund_attention", true},
		{"failed and unpaid", payin("failed", "processing"), "expired", false},
		{"refund review in complete", refundReview, "returning", false},
		{"refund review in transaction", txnReview, "returning", false},
		{"processing and paid", payin("processing", "completed"), "processing", false},
		{"on_hold and paid", payin("on_hold", "completed"), "processing", false},
		{"processing and unpaid", payin("processing", "processing"), "awaiting_funds", false},
		{"on_hold and unpaid", payin("on_hold", "on_hold"), "awaiting_funds", false},
		{"refunded wins over stale refund review", staleRefunded, "refunded", false},
		{"failed and paid wins over stale refund review", staleFailedPaid, "refund_attention", true},
		{"refund review in payment", paymentReview, "returning", false},
		{"failed after a risk review of the payment", payin("failed", "pending_review"), "refund_attention", true},
		{"failed after a refund review of the payment", payin("failed", "pending_refund_review"), "refund_attention", true},
		{"processing with the payment in risk review", payin("processing", "pending_review"), "processing", false},
		{"on_hold with the payment in risk review", payin("on_hold", "pending_review"), "processing", false},
		{"unknown", payin("mystery", "processing"), "processing", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, attention := payinStatus(c.in)
			assert.Equal(t, c.want, got)
			assert.Equal(t, c.attention, attention)
		})
	}
}

// Every PayinOut.tracking_payment.step in the OpenAPI fixture, pinned: a step
// past processing/on_hold means BlindPay holds the payment.
func TestPaymentReceived(t *testing.T) {
	cases := map[string]bool{
		"processing":            false,
		"on_hold":               false,
		"pending_review":        true,
		"pending_refund_review": true,
		"completed":             true,
		"":                      false,
		"mystery":               false,
	}
	for step, want := range cases {
		t.Run(step, func(t *testing.T) {
			assert.Equal(t, want, paymentReceived(step))
		})
	}
}

func TestPayinTxHash(t *testing.T) {
	hash := "5Kx"
	p := payinResponse{TrackingComplete: payinTracking{TransactionHash: &hash}}
	assert.Equal(t, "5Kx", payinTxHash(p))
	assert.Equal(t, "", payinTxHash(payinResponse{}))
}
