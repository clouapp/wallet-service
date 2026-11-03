// Package broadcast fans transaction-status events out to live subscribers
// (the mobile app's SSE connections). Delivery is best-effort by design: the
// transactions table stays the source of truth and the app re-syncs through
// the existing GET endpoints on (re)connect.
package broadcast

import (
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/support/wiretime"
)

// TransactionEvent is the payload pushed to every user who acts for a
// customer when one of its transactions changes status. It carries the
// notification essentials; the app refetches the full view through the
// existing GET endpoints when it needs fields not present here.
//
// Field names follow the model layer (amount_fiat, amount_token, target_token)
// for both directions, which is not what the REST resource calls them on every
// field. The authoritative part of the event is the status-change signal
// (id, type, status); clients needing exact amounts must re-fetch through the
// GET endpoint.
type TransactionEvent struct {
	// CustomerID names whose order it is — a person's own (the user's id) or
	// a company's they are a member of — so the app can tell the two apart
	// (KYB API contract §11.1).
	CustomerID uint `json:"customer_id"`
	// ID is the transaction's external_id (the public identifier used by
	// GET /api/onramps/{id} and /api/offramps/{id}).
	ID     string `json:"id"`
	Type   string `json:"type"`
	Status string `json:"status"`
	// AmountBRL is the fiat leg of a BRL order only (installed apps read it);
	// AmountFiat is the fiat leg in FiatCurrency on every order.
	AmountBRL     string     `json:"amount_brl,omitempty"`
	AmountFiat    string     `json:"amount_fiat"`
	AmountToken   string     `json:"amount_token"`
	TargetToken   string     `json:"target_token"`
	FailureReason *string    `json:"failure_reason,omitempty"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
	// FiatCurrency is the currency of AmountFiat ("brl", "usd"); omitted for
	// a row that has none, which is BRL.
	FiatCurrency string `json:"fiat_currency,omitempty"`
}

// NewTransactionEvent projects a Transaction into its broadcast payload.
func NewTransactionEvent(tx *models.Transaction) TransactionEvent {
	return TransactionEvent{
		CustomerID:    tx.CustomerID,
		ID:            tx.ExternalID,
		Type:          tx.Type,
		Status:        tx.Status,
		AmountBRL:     tx.LegacyAmountBRL(),
		AmountFiat:    tx.AmountFiat,
		AmountToken:   tx.AmountToken,
		TargetToken:   tx.TargetToken,
		FailureReason: tx.FailureReason,
		CompletedAt:   wiretime.Ptr(tx.CompletedAt),
		FiatCurrency:  tx.FiatCurrency,
	}
}

// KycEvent is pushed when a customer's compliance state at one provider
// changes, to every user who acts for it. It exists because document
// submission runs on a worker: the request that started it returns before the
// provider has answered, and this is how the answer reaches the app.
//
// CustomerID names whose state it is — a person's own (the user's id) or a
// company's they are a member of — so the app can tell the two apart (KYB API
// contract §11.1).
//
// Every field here is safe to put on a wire the user is reading. The provider's
// raw snapshot, the submitted documents and Bridge's `developer_reason` are all
// absent by construction — Requirements and RejectionReasons carry only the
// messages the adapter already marked user-shareable.
type KycEvent struct {
	CustomerID uint   `json:"customer_id"`
	Provider   string `json:"provider"`
	KycStatus  string `json:"kyc_status"`
	TosStatus  string `json:"tos_status,omitempty"`
	// CollectionMode tells the app which flow is in force, so it can switch to
	// the hosted link when an API submission fell back to one.
	CollectionMode   string   `json:"collection_mode,omitempty"`
	HostedKycURL     *string  `json:"hosted_kyc_url,omitempty"`
	Requirements     []string `json:"requirements,omitempty"`
	RejectionReasons []string `json:"rejection_reasons,omitempty"`
	// FailureReason is set when the submission itself failed to reach the
	// provider — a distinct outcome from being rejected by one, and the only
	// case where the user should retry rather than fix something.
	FailureReason string `json:"failure_reason,omitempty"`
	// FailureCode says why, in the same vocabulary as the HTTP error codes
	// (ramp.ErrorCode), so the app can tell a provider that is merely busy
	// from a refusal no retry will change — unsigned terms, say. Set only
	// when FailureReason is.
	FailureCode string `json:"failure_code,omitempty"`
}

// Broadcaster delivers transaction and KYC events to a user's live subscribers.
//
// The two streams are separate subscriptions rather than one envelope: a
// subscriber's buffer is per-stream, so a burst of KYC syncs cannot evict a
// transaction the app was waiting for. On the wire they stay one SSE
// connection, discriminated by the frame's event name.
type Broadcaster interface {
	// Publish delivers event to every active subscriber of userID. It never
	// blocks and never returns an error: a subscriber whose buffer is full
	// misses the event (logged), and a publish failure must never affect the
	// caller's persistence flow.
	Publish(userID uint, event TransactionEvent)
	// Subscribe registers a subscriber for userID. The returned function
	// releases the subscription and closes the channel; it is safe to call
	// more than once.
	Subscribe(userID uint) (<-chan TransactionEvent, func())
	// PublishKyc and SubscribeKyc are the same contract for the compliance
	// stream.
	PublishKyc(userID uint, event KycEvent)
	SubscribeKyc(userID uint) (<-chan KycEvent, func())
}
