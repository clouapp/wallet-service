package blindpay

import "github.com/macrowallets/waas/packages/rampengine/app/models"

// payoutTracking is one step of BlindPay's payout pipeline. The fields beyond
// the step are the ones only some steps carry: the collection's status and
// hash, the fiat leg's provider status, the reasons a step names.
type payoutTracking struct {
	Step                string  `json:"step"`
	Status              *string `json:"status"`
	TransactionHash     *string `json:"transaction_hash"`
	ProviderStatus      *string `json:"provider_status"`
	ProviderErrorReason *string `json:"provider_error_reason"`
	RefundReviewReason  *string `json:"refund_review_reason"`
}

// payoutResponse is the slice of BlindPay's PayoutOut the adapter reads.
// Amounts are in cents of their currency.
type payoutResponse struct {
	ID                  string         `json:"id"`
	Status              string         `json:"status"`
	CustomerID          string         `json:"customer_id"`
	QuoteID             string         `json:"quote_id"`
	BankAccountID       string         `json:"bank_account_id"`
	Network             string         `json:"network"`
	Token               string         `json:"token"`
	SenderAmount        quoteNumber    `json:"sender_amount"`
	ReceiverAmount      quoteNumber    `json:"receiver_amount"`
	SenderWalletAddress string         `json:"sender_wallet_address"`
	TrackingTransaction payoutTracking `json:"tracking_transaction"`
	TrackingPayment     payoutTracking `json:"tracking_payment"`
	TrackingComplete    payoutTracking `json:"tracking_complete"`
}

// payoutStatus is the one canonical mapping used by GetBoundOrder and the
// webhooks. The table is evaluated top to bottom (spec 5.8 item 6).
//
// "Tokens collected" is read as tracking_transaction.status = found with a
// transaction_hash. That reading comes from the OpenAPI, not from a sandbox
// run, so it is the one assumption to revisit if a probe says otherwise. A
// payout whose tokens were collected and which then fails owes the user a
// refund, so it is flagged for an operator.
func payoutStatus(p payoutResponse) (status string, attention bool) {
	switch p.Status {
	case "completed":
		return models.TxStatusCompleted, false
	case "refunded":
		return models.TxStatusRefunded, false
	}
	if stringIs(p.TrackingComplete.Status, "tokens_refunded") {
		return models.TxStatusRefunded, false
	}
	if p.Status == "failed" {
		if tokensCollected(p) {
			return models.TxStatusRefundAttention, true
		}
		return models.TxStatusFailed, false
	}
	for _, t := range []payoutTracking{p.TrackingTransaction, p.TrackingPayment, p.TrackingComplete} {
		if t.Step == "pending_refund_review" {
			return models.TxStatusReturning, false
		}
	}
	// The payout is still in flight here, so a fiat leg that came back or
	// failed is money to give back.
	for _, bad := range []string{"returned", "failed", "canceled"} {
		if stringIs(p.TrackingPayment.ProviderStatus, bad) {
			return models.TxStatusRefundAttention, true
		}
	}
	return models.TxStatusProcessing, false
}

// payoutStatusKnown reports whether BlindPay's payout status is one the table
// names; any other reads as processing, and the caller logs it.
func payoutStatusKnown(status string) bool {
	switch status {
	case "processing", "failed", "refunded", "completed", "on_hold":
		return true
	default:
		return false
	}
}

// tokensCollected is the collection having happened: found, with a hash. A
// failed collection, or one without a hash, left the tokens with the user.
func tokensCollected(p payoutResponse) bool {
	t := p.TrackingTransaction
	return stringIs(t.Status, "found") && t.TransactionHash != nil && *t.TransactionHash != ""
}

func stringIs(s *string, want string) bool { return s != nil && *s == want }

// payoutTxHash is the hash of the collection (the tokens leaving the wallet),
// or "" until it exists. The refund's hash stays in the raw payload only.
func payoutTxHash(p payoutResponse) string {
	if h := p.TrackingTransaction.TransactionHash; h != nil {
		return *h
	}
	return ""
}

// payoutFailureReason is the provider's own reason a step failed or went to
// review, or "".
func payoutFailureReason(p payoutResponse) string {
	for _, reason := range []*string{
		p.TrackingTransaction.ProviderErrorReason,
		p.TrackingPayment.ProviderErrorReason,
		p.TrackingPayment.RefundReviewReason,
	} {
		if reason != nil && *reason != "" {
			return *reason
		}
	}
	return ""
}
