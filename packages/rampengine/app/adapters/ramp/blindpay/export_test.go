package blindpay

import (
	"context"
	"time"
)

// SetClock replaces the clock the find-or-create keys are bucketed by.
func SetClock(a *Adapter, now func() time.Time) { a.now = now }

// CustomerResponse exposes the DTO to the contract test in blindpay_test.
type CustomerResponse = customerResponse

// CustomerCreateRequest exposes the create body to the contract test.
type CustomerCreateRequest = customerCreateRequest

// ParseImageDataURI exposes the data: URI decoder to its tests.
var ParseImageDataURI = parseImageDataURI

// PaymentReceived exposes the payment-step reading to the contract test.
var PaymentReceived = paymentReceived

// FeeBps exposes the fee percent conversion to its tests.
var FeeBps = feeBps

// PartnerFeeRequest exposes the partner fee create body to the contract test.
type PartnerFeeRequest = partnerFeeRequest

// PartnerFeeResponse exposes the partner fee DTO to the contract test.
type PartnerFeeResponse = partnerFeeResponse

// PartnerFeeID exposes the find-or-create to its tests.
func PartnerFeeID(a *Adapter, ctx context.Context, c *Client, payin, payout int) (string, error) {
	return a.partnerFeeID(ctx, c, payin, payout)
}

// ForgetPartnerFee exposes the cache eviction to its tests.
func ForgetPartnerFee(a *Adapter, instanceID string, payin, payout int) {
	a.forgetPartnerFee(instanceID, payin, payout)
}

// CreateBlockchainWalletIn exposes the blockchain wallet create body to the contract test.
type CreateBlockchainWalletIn = blockchainWalletRequest

// BlockchainWalletOut exposes the blockchain wallet DTO to the contract test.
type BlockchainWalletOut = blockchainWalletResponse

// EnsureBlockchainWallet exposes the find-or-register to its tests.
func EnsureBlockchainWallet(a *Adapter, ctx context.Context, c *Client, customerID, network, address string) (string, error) {
	return a.ensureBlockchainWallet(ctx, c, customerID, network, address)
}

// ValidSolanaAddress exposes the address check to its tests.
var ValidSolanaAddress = validSolanaAddress

// PayinResponse exposes the payin DTO to the contract test.
type PayinResponse = payinResponse

// CreatePayinQuoteIn exposes the payin quote body to the contract test.
type CreatePayinQuoteIn = payinQuoteRequest

// CreatePayinQuoteOut exposes the payin quote DTO to the contract test.
type CreatePayinQuoteOut = payinQuoteResponse

// CreatePayinIn exposes the payin create body to the contract test.
type CreatePayinIn = payinCreateRequest

// CreatePayinOut exposes the payin create DTO to the contract test.
type CreatePayinOut = payinCreateResponse

// ForgetBlockchainWallet exposes the wallet cache eviction to its tests.
func ForgetBlockchainWallet(a *Adapter, instanceID, customerID, network, address string) {
	a.forgetBlockchainWallet(instanceID, customerID, network, address)
}

// BankAccountRequest and BankAccountResponse expose the bank account DTOs to the contract test.
type BankAccountRequest = bankAccountRequest
type BankAccountResponse = bankAccountResponse

// QuoteRequest and QuoteResponse expose the bank payout quote DTOs to the contract test.
type QuoteRequest = quoteRequest
type QuoteResponse = quoteResponse

// LimitsResponse exposes the customer limits DTO to the contract test.
type LimitsResponse = limitsResponse

// PrepareDelegateRequest and PrepareDelegateResponse expose the prepare-delegate-solana DTOs to the contract test.
type PrepareDelegateRequest = prepareDelegateRequest
type PrepareDelegateResponse = prepareDelegateResponse

// PayoutResponse exposes the payout DTO to the contract test.
type PayoutResponse = payoutResponse
