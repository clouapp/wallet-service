package blindpay

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func str(s string) *string { return &s }

// payoutOf builds a payout with the three pipeline steps the status reads.
func payoutOf(status string, mutate func(*payoutResponse)) payoutResponse {
	p := payoutResponse{Status: status}
	if mutate != nil {
		mutate(&p)
	}
	return p
}

// One row per row of the status table (spec 5.8 item 6), plus the orderings
// that decide which row wins.
func TestPayoutStatus(t *testing.T) {
	collected := func(p *payoutResponse) {
		p.TrackingTransaction = payoutTracking{Status: str("found"), TransactionHash: str("5collect")}
	}
	cases := []struct {
		name      string
		in        payoutResponse
		want      string
		attention bool
	}{
		{"completed", payoutOf("completed", nil), "completed", false},
		{"refunded by status", payoutOf("refunded", nil), "refunded", false},
		{"refunded by tokens_refunded", payoutOf("processing", func(p *payoutResponse) {
			p.TrackingComplete = payoutTracking{Status: str("tokens_refunded")}
		}), "refunded", false},
		{"failed, collection failed", payoutOf("failed", func(p *payoutResponse) {
			p.TrackingTransaction = payoutTracking{Status: str("failed")}
		}), "failed", false},
		{"failed, collection failed even with a hash", payoutOf("failed", func(p *payoutResponse) {
			p.TrackingTransaction = payoutTracking{Status: str("failed"), TransactionHash: str("5x")}
		}), "failed", false},
		{"failed, no hash", payoutOf("failed", nil), "failed", false},
		{"failed, found without a hash", payoutOf("failed", func(p *payoutResponse) {
			p.TrackingTransaction = payoutTracking{Status: str("found")}
		}), "failed", false},
		{"failed, tokens collected", payoutOf("failed", collected), "refund_attention", true},
		{"refund review in transaction", payoutOf("on_hold", func(p *payoutResponse) {
			p.TrackingTransaction = payoutTracking{Step: "pending_refund_review"}
		}), "returning", false},
		{"refund review in payment", payoutOf("processing", func(p *payoutResponse) {
			p.TrackingPayment = payoutTracking{Step: "pending_refund_review"}
		}), "returning", false},
		{"refund review in complete", payoutOf("processing", func(p *payoutResponse) {
			p.TrackingComplete = payoutTracking{Step: "pending_refund_review"}
		}), "returning", false},
		{"payment returned", payoutOf("processing", func(p *payoutResponse) {
			p.TrackingPayment = payoutTracking{ProviderStatus: str("returned")}
		}), "refund_attention", true},
		{"payment failed", payoutOf("on_hold", func(p *payoutResponse) {
			p.TrackingPayment = payoutTracking{ProviderStatus: str("failed")}
		}), "refund_attention", true},
		{"payment canceled", payoutOf("processing", func(p *payoutResponse) {
			p.TrackingPayment = payoutTracking{ProviderStatus: str("canceled")}
		}), "refund_attention", true},
		{"payment sent is just processing", payoutOf("processing", func(p *payoutResponse) {
			p.TrackingPayment = payoutTracking{ProviderStatus: str("sent")}
		}), "processing", false},
		{"processing", payoutOf("processing", nil), "processing", false},
		{"on_hold", payoutOf("on_hold", nil), "processing", false},
		{"unknown value", payoutOf("mystery", nil), "processing", false},
		// Orderings.
		{"completed wins over a returned payment", payoutOf("completed", func(p *payoutResponse) {
			p.TrackingPayment = payoutTracking{ProviderStatus: str("returned")}
		}), "completed", false},
		{"refunded wins over stale refund review", payoutOf("refunded", func(p *payoutResponse) {
			p.TrackingComplete = payoutTracking{Step: "pending_refund_review"}
		}), "refunded", false},
		{"failed with tokens collected wins over refund review", payoutOf("failed", func(p *payoutResponse) {
			collected(p)
			p.TrackingTransaction.Step = "pending_refund_review"
		}), "refund_attention", true},
		{"refund review wins over a returned payment", payoutOf("processing", func(p *payoutResponse) {
			p.TrackingComplete = payoutTracking{Step: "pending_refund_review"}
			p.TrackingPayment = payoutTracking{ProviderStatus: str("returned")}
		}), "returning", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, attention := payoutStatus(c.in)
			assert.Equal(t, c.want, got)
			assert.Equal(t, c.attention, attention)
		})
	}
}

func TestPayoutStatusKnown(t *testing.T) {
	for _, s := range []string{"processing", "failed", "refunded", "completed", "on_hold"} {
		assert.True(t, payoutStatusKnown(s), s)
	}
	assert.False(t, payoutStatusKnown("mystery"))
	assert.False(t, payoutStatusKnown(""))
}

func TestPayoutTxHashIsTheCollectionNotTheRefund(t *testing.T) {
	p := payoutResponse{
		TrackingTransaction: payoutTracking{TransactionHash: str("5collect")},
		TrackingComplete:    payoutTracking{TransactionHash: str("5refund")},
	}
	assert.Equal(t, "5collect", payoutTxHash(p))
	assert.Equal(t, "", payoutTxHash(payoutResponse{TrackingComplete: payoutTracking{TransactionHash: str("5refund")}}))
}

func TestPayoutFailureReason(t *testing.T) {
	assert.Equal(t, "erc20_allowance_insufficient", payoutFailureReason(payoutResponse{
		TrackingTransaction: payoutTracking{ProviderErrorReason: str("erc20_allowance_insufficient")},
		TrackingPayment:     payoutTracking{RefundReviewReason: str("time_window")},
	}))
	assert.Equal(t, "time_window", payoutFailureReason(payoutResponse{
		TrackingPayment: payoutTracking{RefundReviewReason: str("time_window")},
	}))
	assert.Equal(t, "", payoutFailureReason(payoutResponse{}))
}
