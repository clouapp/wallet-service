package ramp

import "context"

// OfframpSettlementAdapter is a provider whose off-ramp collects crypto into its
// own custody before paying out, and therefore needs a second movement to finish
// the order.
//
// It is deliberately not part of ProviderAdapter, for the same reason
// OnrampDeliveryAdapter is not: only one provider has this shape today, and
// folding it into the fat interface would make every other adapter — stubs
// included — implement a refusal nobody will ever ask them for. The service
// type-asserts it off a resolved ProviderAdapter.
//
// Bridge off-ramp is order-first against the user's wallet: create already names
// the deposit address and never holds funds at the provider before payout.
// Ozav off-ramp is wallet-first: create registers Pix and returns managed-wallet
// funding instructions; payout runs only after the credit is there.
type OfframpSettlementAdapter interface {
	Code() ProviderCode
	// OfframpFundingInstructions returns where to send crypto before payout.
	// It must not POST /transfers for the payout leg.
	OfframpFundingInstructions(ctx context.Context, ref CustomerRef, req OfframpFundingRequest) (*OfframpFundingInstructions, error)
	// OfframpFundingSatisfied reports whether spendable balance covers the quoted
	// source amount on the order's chain and token.
	OfframpFundingSatisfied(ctx context.Context, ref CustomerRef, req OfframpFundingRequest) (bool, error)
	// SettleOfframp executes wallet → payout_destination using the persisted
	// provider quote. Idempotent on IdempotencyKey.
	SettleOfframp(ctx context.Context, ref CustomerRef, req OfframpSettleRequest) (*RampOrder, error)
}

// OfframpFundingRequest carries the quote legs needed to resolve a deposit
// address or compare balance against the order amount.
type OfframpFundingRequest struct {
	OfframpCreateRequest
}

// OfframpFundingInstructions is what the user sends before Pix payout can run.
type OfframpFundingInstructions struct {
	ToAddress   string
	PaymentRail string
	Currency    string
	Amount      string
}

// OfframpSettleRequest is the payout leg after funding is satisfied.
type OfframpSettleRequest struct {
	OfframpCreateRequest
	ExternalAccountID string
}
