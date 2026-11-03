package blindpay

// payinTracking is one step of BlindPay's payin pipeline.
type payinTracking struct {
	Step            string  `json:"step"`
	Status          *string `json:"status"`
	TransactionHash *string `json:"transaction_hash"`
}

// payinResponse is the slice of BlindPay's PayinOut the adapter reads.
type payinResponse struct {
	ID                  string        `json:"id"`
	Status              string        `json:"status"`
	CustomerID          string        `json:"customer_id"`
	PayinQuoteID        string        `json:"payin_quote_id"`
	Token               string        `json:"token"`
	Network             string        `json:"network"`
	PixCode             *string       `json:"pix_code"`
	SenderAmount        quoteNumber   `json:"sender_amount"`
	ReceiverAmount      quoteNumber   `json:"receiver_amount"`
	TrackingTransaction payinTracking `json:"tracking_transaction"`
	TrackingPayment     payinTracking `json:"tracking_payment"`
	TrackingComplete    payinTracking `json:"tracking_complete"`
}

// payinStatus is the one canonical mapping used by create, GetOrder, webhooks
// and reconcile. The table is evaluated top to bottom.
//
// "Paid" is read from tracking_payment.step: see paymentReceived. The S9 probe
// has not yet confirmed which steps mean the Pix was received, so this is the
// one assumption to revisit if the probe says otherwise.
func payinStatus(p payinResponse) (status string, attention bool) {
	paid := paymentReceived(p.TrackingPayment.Step)
	switch p.Status {
	case "completed":
		return "completed", false
	case "refunded":
		return "refunded", false
	case "failed":
		if paid {
			return "refund_attention", true
		}
		return "expired", false
	}
	for _, t := range []payinTracking{p.TrackingTransaction, p.TrackingPayment, p.TrackingComplete} {
		if t.Step == "pending_refund_review" {
			return "returning", false
		}
	}
	switch p.Status {
	case "processing", "on_hold":
		if paid {
			return "processing", false
		}
		return "awaiting_funds", false
	}
	return "processing", false
}

// paymentReceived reports whether a payment step is past processing/on_hold.
// A risk review (pending_review) or a refund review (pending_refund_review) is
// a review of a payment BlindPay already holds, so a failure from there is
// money to give back, not an unpaid window. An unknown step counts as unpaid.
func paymentReceived(step string) bool {
	switch step {
	case "completed", "pending_review", "pending_refund_review":
		return true
	default:
		return false
	}
}

// payinTxHash is the on-chain hash of the delivery, or "" until it exists.
func payinTxHash(p payinResponse) string {
	if h := p.TrackingComplete.TransactionHash; h != nil {
		return *h
	}
	return ""
}
