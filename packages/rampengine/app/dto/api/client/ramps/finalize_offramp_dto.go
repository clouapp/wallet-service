package ramps

// FinalizeOfframpDTO is the wallet's signed funding action for a bank
// payout, which the backend sends to the provider to create the payout.
type FinalizeOfframpDTO struct {
	// OrderID is the order's external id, from the path.
	OrderID string
	// SignedTransaction is the base64 of exactly what the wallet signed.
	SignedTransaction string
	// ActionRevision is the revision of the funding action the wallet signed.
	ActionRevision int
	// IdempotencyKey is the client's Idempotency-Key header, up to 128
	// characters. A retry with the same key and body answers the same order.
	IdempotencyKey string
} //	@name	ClientFinalizeOfframpDTO
