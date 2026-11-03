package ramp

import (
	"encoding/json"
	"time"
)

// FiatLeg is the BRL/PIX side of a ramp. Amount is a decimal string; floats
// never touch money in this stack.
type FiatLeg struct {
	Rail     string
	Currency string
	Amount   string
}

// CryptoLeg is the self-custody side of a ramp. PaymentRail is the chain
// (`ethereum`, `solana`, …), Currency the token (`usdc`).
type CryptoLeg struct {
	Currency    string
	PaymentRail string
	Amount      string
	// Address is the user's wallet: the destination on-ramp, the source
	// off-ramp.
	Address string
}

// PixDestination is where an off-ramp pays out.
type PixDestination struct {
	Rail     string
	Currency string
	PixKey   string
	// PixKeyType is `cpf`, `cnpj`, `email`, `phone` or `evp`.
	PixKeyType string
	// AccountOwnerName is who the key belongs to. Providers that register a
	// payout destination as its own object require it, and only the caller
	// knows it — the key itself is often just a number.
	AccountOwnerName string
	// RecipientDocument is the CPF or CNPJ (digits, and letters for an
	// alphanumeric CNPJ) for phone, email, or evp keys.
	// For cpf/cnpj key types the provider infers it from PixKey instead.
	RecipientDocument string
	// ExternalAccountID is the id (ba_…) of the destination registered at a
	// provider that pays a Pix out of a prepared funding. The service fills it
	// before quoting; the other providers ignore it.
	ExternalAccountID string
}

// OnrampQuoteRequest prices BRL in → crypto out. FeePercent is XIP's cut for
// the rail, read from `rail_routing_settings`; the adapter translates it to
// whatever the provider calls it.
type OnrampQuoteRequest struct {
	Source      FiatLeg
	Destination CryptoLeg
	FeePercent  string
}

// OnrampCreateRequest places the order. ProviderQuoteID is set only when the
// provider's quote is binding and the service is redeeming it.
type OnrampCreateRequest struct {
	OnrampQuoteRequest
	ProviderQuoteID string
	// IdempotencyKey is derived from the transaction's external id and
	// persisted before this call, so a retry reaches the provider unchanged.
	IdempotencyKey string
}

// OfframpQuoteRequest prices crypto in → fiat out. Destination is the Pix key
// on pix_payout; Bank is the registered bank account on the bank rails. The
// rail decides which one is set, and ValidateDestination holds the two to it.
type OfframpQuoteRequest struct {
	Source      CryptoLeg
	Destination PixDestination
	Bank        *BankDestination
	FeePercent  string
}

// OfframpCreateRequest places the payout order.
type OfframpCreateRequest struct {
	OfframpQuoteRequest
	ProviderQuoteID string
	// ExternalAccountID is the provider-side PIX account, when the service
	// resolved or created one before the transfer.
	ExternalAccountID string
	IdempotencyKey    string
	// QuoteExpiresAt is the bound estimate expiry when create redeems a quote.
	QuoteExpiresAt *time.Time
	// OfframpFundingWindowMinutes caps how long deposit instructions stay valid.
	// Zero means the adapter default (30).
	OfframpFundingWindowMinutes int
}

// Estimate is what an adapter knows about a quote. The XIP-side id and the
// expiry/consumption bookkeeping belong to the ramp services, which persist
// `ramp_estimates`; an adapter never sets an XIP id.
type Estimate struct {
	// ProviderQuoteID is empty for providers whose estimates are indicative.
	ProviderQuoteID string
	// Binding reports whether create must redeem this exact quote.
	Binding   bool
	ExpiresAt *time.Time
	Rate      string
	// FeePercent echoes the rail fee applied.
	FeePercent string
	FeeAmount  string
	// FeeCurrency is the currency the provider charges the fee in: `brl` for
	// Bridge's and Ozav's on-ramps, the token symbol for their off-ramps and
	// for BlindPay's partner fee. Without it FeeAmount is unreadable.
	FeeCurrency      string
	EstimatedReceive string
	// ProviderDeductedAmount is what the provider itself took out of the source
	// leg before converting — network and Pix costs — when its quote says so.
	// It is already out of EstimatedReceive: the caller reports it and never
	// subtracts it again. Empty when the provider states none (Bridge never
	// does; Ozav's sandbox does not).
	ProviderDeductedAmount string
	// ProviderDeductedCurrency is the currency of ProviderDeductedAmount, the
	// source leg's (`brl` on-ramp, the token symbol off-ramp), lowercase like
	// FeeCurrency. Empty exactly when ProviderDeductedAmount is.
	ProviderDeductedCurrency string
	// ProviderFeePercent and ProviderFeeAmount name what the provider's own
	// conversion takes, beyond XIP's fee: the gap between the rate applied and
	// the market's, as a percent and in the currency the quote pays out in (reais on Pix,
	// dollars on the bank rails). Empty when the provider reports
	// no gap, or quotes net without naming it.
	ProviderFeePercent string
	ProviderFeeAmount  string
	// SourceAmount is the amount this quote priced, written as the provider
	// takes it at create; empty means the request's amount as written. It
	// differs when the provider moves fewer places than the token has. The
	// caller shows it and binds the create to it — it is never substituted
	// after the user saw the quote.
	SourceAmount string
	Raw          json.RawMessage
}

// RampOrder is a created order. Status is already canonical — the adapter maps
// the provider's own vocabulary before returning.
type RampOrder struct {
	ProviderOrderID string
	Status          string
	// DepositInstructions is what the user needs to fund the order (a PIX
	// br_code on-ramp, a deposit address off-ramp).
	DepositInstructions map[string]any
	// Amounts carries the legs as decimal strings, keyed by the adapter.
	Amounts     map[string]string
	FeePercent  string
	FeeAmount   string
	FeeCurrency string
	// TxHash is the on-chain settlement, present once the crypto leg moved. A
	// refresh carries it so an order whose webhook was lost still gets one.
	TxHash string
	// ProviderDestinationRef is a provider-side handle for the destination —
	// a registered payout account, say. Persisting it lets the next order to
	// the same destination name it instead of registering a second one.
	ProviderDestinationRef string
	// ExpiresAt is when the deposit instructions stop being payable.
	ExpiresAt *time.Time
	// DeliveryFailed is set when a two-phase on-ramp's delivery transfer failed
	// or was cancelled while the pay-in remains settled.
	DeliveryFailed bool
	// FailureReason carries the provider's detail for any transfer it reports
	// failed or cancelled, the delivery of a two-phase on-ramp included.
	FailureReason string
	Raw           json.RawMessage
}
