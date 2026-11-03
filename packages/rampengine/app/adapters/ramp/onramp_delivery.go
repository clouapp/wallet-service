package ramp

import (
	"context"
	"time"
)

// OnrampDeliveryAdapter is a provider whose on-ramp lands the crypto in its own
// custody rather than at the user's address, and which therefore needs a second
// movement to finish the order.
//
// It is deliberately not part of ProviderAdapter, for the same reason
// VirtualAccountAdapter is not: only one provider has this shape, and folding
// it into the fat interface would make every other adapter — stubs included —
// implement a refusal nobody will ever ask them for. The service type-asserts
// it off a resolved ProviderAdapter, and a provider that does not implement it
// simply has nothing left to do once its pay-in settles.
//
// Whether a provider needs this is a property of how it collects, not a choice:
//
//   - Bridge delivers straight to the address the account was provisioned
//     against, so its on-ramp is finished when the deposit settles.
//   - Ozav's on-ramp is `fiat_payment -> wallet` and nothing else —
//     `fiat_payment -> crypto_address` is a measured 422
//     UNSUPPORTED_COMBINATION — so the crypto arrives in the customer's Ozav
//     wallet and has to be sent on.
type OnrampDeliveryAdapter interface {
	Code() ProviderCode
	// DeliverOnramp moves crypto the on-ramp bought out of the provider's
	// custody and into the user's own wallet.
	//
	// It is called only after the pay-in has settled, because there is nothing
	// to move before that. Implementations must be idempotent on
	// IdempotencyKey: this is a real, irreversible send, and the caller retries.
	DeliverOnramp(ctx context.Context, ref CustomerRef, req OnrampDeliveryRequest) (*RampOrder, error)
}

// OnrampDeliveryRequest is the second movement of an order-first on-ramp.
type OnrampDeliveryRequest struct {
	// Amount is the crypto to send, as the pay-in actually delivered it — read
	// from the settled order, never recomputed from the quote.
	Amount string
	// Destination is the user's own wallet.
	Destination CryptoLeg
	// AttestedOwnershipAt is when the user asserted the destination is theirs.
	// Brazil's Resolution 520 asks for it on the beneficiary, and the honest
	// answer is the moment they named the address on the order — not the
	// moment we happen to send.
	AttestedOwnershipAt time.Time
	// IdempotencyKey is derived from the transaction and persisted before the
	// call, so a retry reaches the provider unchanged.
	IdempotencyKey string
}
