package ramp

import (
	"context"
	"fmt"
	"strings"
)

// BankAccountStatus is where a registered bank account stands at the
// provider. Only an approved account can be quoted against.
type BankAccountStatus string

const (
	BankAccountVerifying  BankAccountStatus = "verifying"
	BankAccountApproved   BankAccountStatus = "approved"
	BankAccountRejected   BankAccountStatus = "rejected"
	BankAccountDeprecated BankAccountStatus = "deprecated"
)

// BeneficiaryAccountRegistration is the only type of the contract that carries a
// bank account in full. It exists only between the vault (E3) and the
// provider call, and String never prints a number, a name or an address.
type BeneficiaryAccountRegistration struct {
	// Name is the deterministic name (BankAccountName) the account is found
	// by when the idempotency window has passed.
	Name             string
	Rail             Rail
	Relationship     string
	AccountClass     string
	AccountType      string
	BeneficiaryName  string
	RoutingNumber    string
	AccountNumber    string
	AddressLine1     string
	AddressLine2     string
	City             string
	StateCode        string
	PostalCode       string
	BusinessIndustry string
}

// String keeps a registration out of logs: only its name and rail.
func (r BeneficiaryAccountRegistration) String() string {
	return fmt.Sprintf("BeneficiaryAccountRegistration{Name:%s Rail:%s}", r.Name, r.Rail)
}

// GoString covers %#v, which ignores String.
func (r BeneficiaryAccountRegistration) GoString() string { return r.String() }

// Format covers %v and %+v on the value and on a pointer.
func (r BeneficiaryAccountRegistration) Format(f fmt.State, _ rune) { fmt.Fprint(f, r.String()) }

// BankAccountResult is a provider bank account as the service keeps it:
// its id, status and the last four digits the echo check compares.
type BankAccountResult struct {
	ExternalAccountID string
	Status            BankAccountStatus
	AccountLast4      string
	RoutingLast4      string
}

// BankAccountRegistrar is a provider that registers a customer's bank
// account before it can be quoted against. Optional, found by type
// assertion like AtomicKycCreator.
type BankAccountRegistrar interface {
	// RegisterBankAccount creates the account. The same registration must
	// produce the same request bytes, so a replay with idempotencyKey is
	// recognised by the provider.
	RegisterBankAccount(ctx context.Context, ref CustomerRef, reg BeneficiaryAccountRegistration, idempotencyKey string) (*BankAccountResult, error)
	GetBankAccount(ctx context.Context, ref CustomerRef, externalID string) (*BankAccountResult, error)
	// FindBankAccountByName answers (nil, nil) when the customer has no
	// account by that name.
	FindBankAccountByName(ctx context.Context, ref CustomerRef, name string) (*BankAccountResult, error)
}

// BankAccountName is the deterministic name of one revision of one
// beneficiary on one rail: xip-{beneficiary}-r{revision}-{ach|wire}.
func BankAccountName(beneficiaryExternalID string, revision int, rail Rail) string {
	suffix := strings.TrimSuffix(string(rail), "_payout")
	return fmt.Sprintf("xip-%s-r%d-%s", beneficiaryExternalID, revision, suffix)
}
