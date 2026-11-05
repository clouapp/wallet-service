package ramp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// ErrLiquidationAddressUnsupported is a provider that does not pay out through
// a standing crypto address. Like ErrStandingAccountUnsupported it is a routing
// signal: the provider was never asked to do anything, so the router may fail
// over to the next one.
var ErrLiquidationAddressUnsupported = errors.New("ramp: provider does not pay out through a liquidation address")

// LiquidationAddressAdapter is a provider that pays fiat out of a permanent
// crypto address: every deposit into it is drained to one bank account. It is
// the off-ramp mirror of VirtualAccountAdapter and, like it, stays out of
// ProviderAdapter: only Bridge serves this shape, and the service type-asserts
// it (PayoutAddress).
type LiquidationAddressAdapter interface {
	Code() ProviderCode
	// RegisterBankAccount records the user's bank account as a payout
	// destination at the provider and returns the provider's id for it.
	RegisterBankAccount(ctx context.Context, ref CustomerRef, req BankAccountRegistration) (*RegisteredBankAccount, error)
	// DeactivateBankAccount stops the provider paying the registered account
	// (externalAccountID, from RegisterBankAccount): the user deleted it. The
	// provider keeps the record.
	DeactivateBankAccount(ctx context.Context, ref CustomerRef, externalAccountID string) error
	// ProvisionLiquidationAddress returns the address that pays req's bank
	// account over req's rail, creating one only when none exists.
	ProvisionLiquidationAddress(ctx context.Context, ref CustomerRef, req LiquidationAddressRequest) (*LiquidationAddress, error)
	// UpdateLiquidationAddress changes the reference or the fee of an existing
	// address. The rail and the address itself never change.
	UpdateLiquidationAddress(ctx context.Context, ref CustomerRef, addressID string, req LiquidationAddressUpdate) (*LiquidationAddress, error)
	// GetDrain re-reads one drain of an address: the reconciliation path.
	GetDrain(ctx context.Context, ref CustomerRef, addressID, drainID string) (*LiquidationDrain, error)
	// SimulateLiquidationDeposit asks a sandbox to pretend a deposit arrived and
	// returns the drain it opened.
	SimulateLiquidationDeposit(ctx context.Context, ref CustomerRef, addressID string, req SimulatedDeposit) (string, error)
}

// PayoutAddress is adapter's liquidation-address port. A provider without one
// is refused with ErrLiquidationAddressUnsupported wrapped, which the router
// fails over on and the client controllers map with errors.Is.
func PayoutAddress(adapter ProviderAdapter) (LiquidationAddressAdapter, error) {
	payout, ok := adapter.(LiquidationAddressAdapter)
	if !ok {
		return nil, fmt.Errorf("provider %q: %w", adapter.Code(), ErrLiquidationAddressUnsupported)
	}
	return payout, nil
}

// BankAccountAddress is declared in models (repositories cannot import ramp)
// and aliased here, the way ProviderCode is.
type BankAccountAddress = models.BankAccountAddress

// BankAccountRegistration is a US bank account as the provider registers it.
type BankAccountRegistration struct {
	// OwnerName is the holder as the user typed it: 3–35 characters, at least
	// two words. First and last are what Bridge requires of an individual.
	OwnerName      string
	OwnerFirstName string
	OwnerLastName  string
	BankName       string
	// AccountName is the user's own label for the account.
	AccountName   string
	RoutingNumber string
	AccountNumber string
	// AccountType is "checking" or "savings".
	AccountType    string
	Address        BankAccountAddress
	IdempotencyKey string
}

// RegisteredBankAccount is the provider's answer to a registration.
type RegisteredBankAccount struct {
	ExternalAccountID string
	// PaymentRails is Bridge's payment_rails.supported, nil when the response
	// omits it. Nil is not a refusal: the field is documented only for FedNow.
	PaymentRails []string
	Raw          json.RawMessage
}

// LiquidationAddressRequest provisions the address that pays one bank account.
type LiquidationAddressRequest struct {
	// Rail is RailUSDACHPayout or RailUSDWirePayout.
	Rail Rail
	// Source is what the user sends: Currency is the token, PaymentRail the
	// chain. Address is unused.
	Source            CryptoLeg
	ExternalAccountID string
	// ReturnAddress is the user's wallet, where a failed drain is sent back.
	ReturnAddress string
	// Reference is the ACH reference or the wire message; "" for none.
	Reference      string
	FeePercent     string
	IdempotencyKey string
}

// LiquidationAddressUpdate changes an existing address.
type LiquidationAddressUpdate struct {
	// Reference nil leaves it; "" clears it (sent as JSON null).
	Reference  *string
	FeePercent string
	// ReturnAddress "" leaves it; anything else is the wallet a drain the
	// provider cannot pay out is sent back to from now on.
	ReturnAddress string
}

// LiquidationAddress is a standing payout address, normalized.
type LiquidationAddress struct {
	ProviderAddressID string
	// Address is where the user sends the crypto.
	Address string
	// Status is models.LiquidationAddressStatusActive or Inactive.
	Status            string
	Rail              Rail
	Chain             string
	Currency          string // token, lowercase
	ExternalAccountID string
	Reference         string
	FeePercent        string
	// ReturnAddress is the wallet a failed drain goes back to, "" when the
	// provider did not say.
	ReturnAddress string
	Raw           json.RawMessage
}

// SimulatedDeposit is a sandbox deposit into an address.
type SimulatedDeposit struct {
	Currency       string
	Amount         string
	IdempotencyKey string
}

// LiquidationDrain is one drain's lifecycle state, whether it arrived on a
// webhook or was re-read from the address's history.
type LiquidationDrain struct {
	DrainID           string
	ProviderAddressID string
	// Status is a canonical transaction status.
	Status string
	// Returning is the provider saying the money is coming back after the
	// payout was sent (Bridge's returned, refund_in_flight, refund_failed):
	// the bank refused it. It is what lets a completed payout move again.
	Returning bool
	// SourceAmount is the crypto deposited (drain.amount).
	SourceAmount   string
	SourceCurrency string // token, lowercase
	// DestinationAmount is the USD sent (receipt.outgoing_amount), "" until known.
	DestinationAmount string
	// FeeAmount is the developer fee the provider took (receipt.developer_fee).
	FeeAmount string
	// PaymentRail is the bank rail it paid over: "ach" or "wire".
	PaymentRail   string
	DepositTxHash string
	FromAddress   string
	// BankReference is the wire's imad or the ACH trace number.
	BankReference string
	FailureReason string
	OccurredAt    *time.Time
	// Raw is the untouched payload. It names the user's bank account and is
	// never logged.
	Raw json.RawMessage
}
