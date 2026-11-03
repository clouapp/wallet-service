package ramps

import "time"

// The outcomes an operator's action on the attention queue records.
const (
	// AttentionOutcomeReconciled links the customer the operator found at the
	// provider to a KYC intent held for review.
	AttentionOutcomeReconciled = "reconciled"
	// AttentionOutcomeCancelled abandons a KYC intent held for review.
	AttentionOutcomeCancelled = "cancelled"
	// AttentionOutcomeFinalized binds the payout the operator found to a
	// finalization.
	AttentionOutcomeFinalized = "finalized"
	// AttentionOutcomeNotCreated fails a finalization whose payout the
	// provider never created.
	AttentionOutcomeNotCreated = "not_created"
)

// ResolveKycIntentDTO is an operator's verdict on a KYC intent held for
// manual review: exactly one of the two.
type ResolveKycIntentDTO struct {
	// CustomerID is the provider customer the operator found; it is linked.
	CustomerID string `json:"customer_id"`
	// Abandon cancels the intent, so the person can submit again.
	Abandon bool `json:"abandon"`
} //	@name	AdminResolveKycIntentDTO

// ResolveFinalizationDTO is an operator's verdict on a bank payout finalize
// the recovery handed over: exactly one of the two.
type ResolveFinalizationDTO struct {
	// PayoutID is the provider payout the operator found; it is bound only
	// when it matches the stored binding.
	PayoutID string `json:"payout_id"`
	// NotCreated says the provider never created the payout; taken only once
	// the quote lapsed and the provider's listing answers without one.
	NotCreated bool `json:"not_created"`
} //	@name	AdminResolveFinalizationDTO

// SupportTicketDTO is the ticket an operator opened with the provider about a
// held payout: a reference of up to 64 characters and a one-line note of up to
// 500.
type SupportTicketDTO struct {
	Reference string `json:"reference"`
	Note      string `json:"note"`
} //	@name	AdminSupportTicketDTO

// AttentionResolutionDTO is what an operator's verdict did: the item it closed,
// its holder and the outcome. PayoutID is set when a payout was bound.
type AttentionResolutionDTO struct {
	Kind          string `json:"kind"`
	ID            string `json:"id"`
	CustomerID    uint   `json:"customer_id"`
	TransactionID string `json:"transaction_id,omitempty"`
	Outcome       string `json:"outcome"`
	PayoutID      string `json:"payout_id,omitempty"`
} //	@name	AdminAttentionResolutionDTO

// SupportTicketRecordedDTO is the ticket as recorded on the transaction.
type SupportTicketRecordedDTO struct {
	TransactionID string    `json:"transaction_id"`
	CustomerID    uint      `json:"customer_id"`
	Status        string    `json:"status"`
	Reference     string    `json:"reference"`
	Note          string    `json:"note"`
	At            time.Time `json:"at"`
} //	@name	AdminSupportTicketRecordedDTO
