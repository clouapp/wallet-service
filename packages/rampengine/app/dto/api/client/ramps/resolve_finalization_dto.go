package ramps

// ResolveFinalizationDTO is an operator's verdict on a bank payout finalize
// the recovery could not close: exactly one of the two. The admin action
// (E8) copies its request into it.
type ResolveFinalizationDTO struct {
	// PayoutID is the provider payout the operator found for the order; it
	// is bound only when a read of it matches the stored binding.
	PayoutID string
	// NotCreated says the provider never created the payout; taken only once
	// the quote has lapsed and the provider's listing answers without one.
	NotCreated bool
} //	@name	ClientResolveFinalizationDTO
