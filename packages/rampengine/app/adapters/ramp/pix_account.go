package ramp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
)

// The two relationships a Pix account has to its customer. first_party is a
// key held under the customer's own document.
const (
	RelationshipFirstParty = "first_party"
	RelationshipOther      = "other"
)

// PixAccountRegistration is the only type of the contract that carries a Pix
// key in full. It exists only between the service and the provider call, and
// String never prints a key, a name or a document.
type PixAccountRegistration struct {
	// Name is the deterministic name (PixAccountName) the account is found by
	// when the idempotency window has passed.
	Name string
	// PixKeyType is cpf, cnpj, email, phone or evp.
	PixKeyType        string
	PixKey            string
	RecipientName     string
	RecipientDocument string
	Relationship      string
}

// String keeps a registration out of logs: only its name.
func (r PixAccountRegistration) String() string {
	return fmt.Sprintf("ramp.PixAccountRegistration{name: %s}", r.Name)
}

// GoString covers %#v, which ignores String.
func (r PixAccountRegistration) GoString() string { return r.String() }

// Format covers %v and %+v on the value and on a pointer.
func (r PixAccountRegistration) Format(f fmt.State, _ rune) { fmt.Fprint(f, r.String()) }

// PixAccountResult is a provider Pix account as the service keeps it.
type PixAccountResult struct {
	ExternalAccountID string
	Status            BankAccountStatus
}

// PixAccountRegistrar is a provider that registers the Pix destination of a
// payout as an object of its own before it can be quoted against. Optional,
// found by type assertion like BankAccountRegistrar.
type PixAccountRegistrar interface {
	// RegisterPixAccount creates the account. The same registration must
	// produce the same request bytes, so a replay under idempotencyKey is
	// recognised by the provider.
	RegisterPixAccount(ctx context.Context, ref CustomerRef, reg PixAccountRegistration, idempotencyKey string) (*PixAccountResult, error)
	GetPixAccount(ctx context.Context, ref CustomerRef, externalID string) (*PixAccountResult, error)
	// FindPixAccountByName answers (nil, nil) when the customer has no
	// account by that name.
	FindPixAccountByName(ctx context.Context, ref CustomerRef, name string) (*PixAccountResult, error)
}

// PixAccountName is the deterministic name of one key under one document:
// xip-pix- and 20 hex digits derived from the two keyed hashes. It holds no
// user data.
func PixAccountName(pixKeyHash, documentHash string) string {
	sum := sha256.Sum256([]byte(pixKeyHash + ":" + documentHash))
	return "xip-pix-" + hex.EncodeToString(sum[:])[:20]
}

// conditionsNotTheData are the failures a 400/422 may carry that say
// something other than "this account's data is wrong".
var conditionsNotTheData = []error{
	ErrProviderCredentialsInvalid, ErrProviderNotImplemented, ErrKycPending,
	ErrIntegrationRequired, ErrTosRequired, ErrIdempotencyConflict,
	ErrRetryableUpstream, ErrOutcomeUnknown, ErrRequestNotSent, ErrBankAccountEchoMismatch,
}

// RefusesTheData is a provider answering 400 or 422 to the registration of a
// payout account with no condition of its own: the account as the user typed
// it. Bank and Pix registrations share the reading.
func RefusesTheData(err error) bool {
	for _, known := range conditionsNotTheData {
		if errors.Is(err, known) {
			return false
		}
	}
	var status interface{ HTTPStatus() int }
	if !errors.As(err, &status) {
		return false
	}
	return status.HTTPStatus() == http.StatusBadRequest || status.HTTPStatus() == http.StatusUnprocessableEntity
}
