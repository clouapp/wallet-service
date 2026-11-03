package ramp

import "context"

// OrderBinding is what a payout must still be, read back from the provider.
// It is written on the funding intent when the payout is finalized, and every
// later read of that payout is checked against it: a provider answer that
// names another quote, account, customer or amount is never applied.
type OrderBinding struct {
	ProviderCustomerID string // re_…
	BankAccountID      string // ba_…
	QuoteID            string // qu_…
	Network, Token     string // provider vocabulary
	SenderAmount       string // the quote's own spelling
	SenderWallet       string
}

// FinalizeRequest is the signed payout the wallet authorized.
type FinalizeRequest struct {
	OrderBinding
	SignedTransaction string // base64, exactly what the wallet signed
	IdempotencyKey    string // provider_finalize_key
}

// FindByQuoteRequest looks a payout up by the quote it redeemed.
type FindByQuoteRequest struct{ OrderBinding }

// OfframpFinalizeAdapter is a provider that creates a bank payout from a
// wallet-signed transaction. Optional, found by type assertion like
// OfframpPrepareAdapter; a provider without it refuses the finalize.
type OfframpFinalizeAdapter interface {
	// FinalizeOfframp creates the payout. It never retries by itself.
	FinalizeOfframp(ctx context.Context, ref CustomerRef, req FinalizeRequest) (*RampOrder, error)
	// FindOfframpByQuote is (nil, nil) only when the provider answered the
	// listing and the payout is not in it.
	FindOfframpByQuote(ctx context.Context, ref CustomerRef, req FindByQuoteRequest) (*RampOrder, error)
}

// BoundOrderReader is a provider that reads one payout and checks it against
// the binding recorded for it. Optional, found by type assertion.
type BoundOrderReader interface {
	// GetBoundOrder reads a payout and refuses one that is not what binding
	// says (ErrBindingMismatch), applying nothing.
	GetBoundOrder(ctx context.Context, ref CustomerRef, providerOrderID string, binding OrderBinding) (*RampOrder, error)
}

// PayoutLegNamer is a provider that names the network and token of a payout's
// crypto leg in its own vocabulary, which is how OrderBinding spells them.
// Optional, found by type assertion. The finalize asks it once, when it
// writes the binding, so every later bound read compares the same spelling.
type PayoutLegNamer interface {
	// PayoutLeg translates the client's chain and token (solana, usdc) into
	// what the provider's payout answers.
	PayoutLeg(ctx context.Context, chain, token string) (network, providerToken string, err error)
}
