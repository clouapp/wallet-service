// Package ramps carries the DTOs of the admin ramp surfaces.
package ramps

import "time"

// Attention kinds: one per source of the queue.
const (
	AttentionKindRefund            = "refund_attention"
	AttentionKindReturningOverdue  = "returning_overdue"
	AttentionKindFinalization      = "finalization"
	AttentionKindKycIntent         = "kyc_intent"
	AttentionKindBindingDivergence = "binding_divergence"
)

// AttentionKinds lists every kind, so a count exists for each of them.
var AttentionKinds = []string{
	AttentionKindRefund, AttentionKindReturningOverdue, AttentionKindFinalization,
	AttentionKindKycIntent, AttentionKindBindingDivergence,
}

// AttentionPerSourceLimit caps each source of the queue.
const AttentionPerSourceLimit = 100

// AdminListAttentionDTO is what the queue is read with. Limit caps each
// source; zero, or more than AttentionPerSourceLimit, means the cap.
type AdminListAttentionDTO struct {
	Limit int `json:"limit"`
} //	@name	AdminListAttentionDTO

// AttentionTicketDTO is the support ticket recorded against a held refund.
type AttentionTicketDTO struct {
	Ref  string     `json:"ref"`
	Note string     `json:"note,omitempty"`
	At   *time.Time `json:"at,omitempty"`
} //	@name	AdminAttentionTicketDTO

// AttentionDetailDTO is the small, per-kind object of an item. It never
// carries amounts, addresses, last4 or anything encrypted.
type AttentionDetailDTO struct {
	PayoutID              *string             `json:"payout_id,omitempty"`
	CollectionHash        *string             `json:"collection_hash,omitempty"`
	SupportTicket         *AttentionTicketDTO `json:"support_ticket,omitempty"`
	Code                  *string             `json:"code,omitempty"`
	State                 *string             `json:"state,omitempty"`
	FailureCode           *string             `json:"failure_code,omitempty"`
	RecoveryAttempts      *int                `json:"recovery_attempts,omitempty"`
	RecoveryNextAttemptAt *time.Time          `json:"recovery_next_attempt_at,omitempty"`
} //	@name	AdminAttentionDetailDTO

// AttentionItemDTO is one thing waiting for an operator. ID is the external id
// the resolving action will receive.
type AttentionItemDTO struct {
	Kind          string
	ID            string
	CustomerID    uint
	TransactionID string
	Provider      string
	Since         time.Time
	AgeSeconds    int64
	Detail        AttentionDetailDTO
} //	@name	AdminAttentionItemDTO

// AttentionQueueDTO is the whole queue, oldest first, with a count per kind.
type AttentionQueueDTO struct {
	Items  []AttentionItemDTO
	Counts map[string]int
} //	@name	AdminAttentionQueueDTO
