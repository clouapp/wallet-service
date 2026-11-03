package swaps

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/swap"
	"github.com/macrowallets/waas/pkg/amount"
)

// Quote includes executable pricing while keeping provider payloads off the wire.
type Quote struct {
	ID                  uuid.UUID      `json:"id"`
	SourceWalletID      uuid.UUID      `json:"source_wallet_id"`
	DestinationWalletID uuid.UUID      `json:"destination_wallet_id"`
	Provider            string         `json:"provider"`
	From                swap.Asset     `json:"from"`
	To                  swap.Asset     `json:"to"`
	Amount              string         `json:"amount"`
	AmountRaw           string         `json:"amount_raw"`
	Expected            string         `json:"expected_amount"`
	ExpectedRaw         string         `json:"expected_amount_raw"`
	Minimum             string         `json:"minimum_amount"`
	MinimumRaw          string         `json:"minimum_amount_raw"`
	ExpiresAt           time.Time      `json:"expires_at"`
	EstimatedSeconds    int            `json:"estimated_seconds"`
	Fees                []swap.Fee     `json:"fees"`
	Attempts            []swap.Attempt `json:"attempts"`
}

// NewQuote renders an already validated quote without its opaque route or credentials.
func NewQuote(row *models.SwapQuote) Quote {
	var request swap.QuoteRequest
	_ = json.Unmarshal([]byte(row.RequestPayload), &request)
	var fees []swap.Fee
	_ = json.Unmarshal([]byte(row.FeesPayload), &fees)
	var attempts []swap.Attempt
	_ = json.Unmarshal([]byte(row.AttemptsPayload), &attempts)
	if fees == nil {
		fees = []swap.Fee{}
	}
	units, _ := amount.ParseBaseUnits(request.AmountRaw)
	expected, _ := amount.ParseBaseUnits(row.ExpectedRaw)
	minimum, _ := amount.ParseBaseUnits(row.MinimumRaw)
	return Quote{ID: row.ID, SourceWalletID: row.SourceWalletID, DestinationWalletID: row.DestinationWalletID, Provider: row.Provider,
		From: request.From, To: request.To, Amount: amount.FormatBaseUnits(units, request.From.Decimals), AmountRaw: request.AmountRaw,
		Expected: amount.FormatBaseUnits(expected, request.To.Decimals), ExpectedRaw: row.ExpectedRaw, Minimum: amount.FormatBaseUnits(minimum, request.To.Decimals), MinimumRaw: row.MinimumRaw,
		ExpiresAt: row.ExpiresAt, EstimatedSeconds: row.EstimatedSeconds, Fees: fees, Attempts: attempts}
}

// Order exposes only public execution outcomes.
type Order struct {
	ID                  uuid.UUID  `json:"id"`
	QuoteID             uuid.UUID  `json:"quote_id"`
	IdempotencyKey      uuid.UUID  `json:"idempotency_key"`
	Provider            string     `json:"provider"`
	Status              string     `json:"status"`
	SourceWalletID      uuid.UUID  `json:"source_wallet_id"`
	DestinationWalletID uuid.UUID  `json:"destination_wallet_id"`
	SourceHash          string     `json:"source_tx_hash,omitempty"`
	DestinationHash     string     `json:"destination_tx_hash,omitempty"`
	ReceivedRaw         string     `json:"received_amount_raw"`
	RefundRaw           string     `json:"refund_amount_raw"`
	RefundHash          string     `json:"refund_tx_hash,omitempty"`
	FailureCode         string     `json:"failure_code,omitempty"`
	LastCheckedAt       *time.Time `json:"last_checked_at,omitempty"`
}

// NewOrder renders the persisted execution state, never signing or provider payloads.
func NewOrder(row *models.SwapOrder) Order {
	return Order{ID: row.ID, QuoteID: row.QuoteID, IdempotencyKey: row.IdempotencyKey, Provider: row.Provider, Status: row.Status,
		SourceWalletID: row.SourceWalletID, DestinationWalletID: row.DestinationWalletID, SourceHash: row.SourceTxHash, DestinationHash: row.DestinationTxHash, ReceivedRaw: row.ReceivedRaw, RefundRaw: row.RefundRaw, RefundHash: row.RefundTxHash, FailureCode: row.FailureCode, LastCheckedAt: row.LastCheckedAt}
}
