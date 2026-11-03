// Package postal defines the provider-agnostic contract for Brazilian postal
// code (CEP) lookups.
//
// An adapter is a pure HTTP mapper: it translates a CEP into one provider's
// API call and back into the canonical Address. It never touches the
// database or the cache — that is the service layer's job, exactly like
// app/adapters/ramp keeps its adapters to httptest alone.
package postal

import (
	"context"
	"errors"
)

type ProviderCode = string

const (
	ProviderViaCep    ProviderCode = "viacep"
	ProviderBrasilApi ProviderCode = "brasilapi"
)

// Address is the canonical shape returned to the client, independent of
// which provider answered.
type Address struct {
	PostalCode   string // normalized 8 digits, no dash
	StreetLine1  string // logradouro
	Neighborhood string // bairro
	City         string
	Subdivision  string // UF, 2 letters
	Country      string // always "BRA"
}

// Sentinel errors for the postal lookup stack.
var (
	// ErrNotFound means a provider answered and confirmed the postal code
	// does not exist. The router treats this as definitive: it is not
	// retried against the next provider in the order.
	ErrNotFound = errors.New("postal: postal code not found")
	// ErrProviderNotImplemented mirrors ramp.ErrProviderNotImplemented for a
	// code with no registered adapter.
	ErrProviderNotImplemented = errors.New("postal: provider not implemented")
	// ErrNoProviderAvailable means every configured provider was skipped
	// (disabled or unregistered).
	ErrNoProviderAvailable = errors.New("postal: no provider available")
	// ErrProviderFailed marks a lookup the provider did not answer usefully:
	// unreachable, a non-2xx other than a definitive not-found, or a body this
	// side could not read. It is the mirror of ramp.ErrProviderFailed, kept
	// here because a CEP lookup has no business importing the payment stack,
	// and it is what the HTTP layer answers 502 for.
	ErrProviderFailed = errors.New("postal: provider call failed")
)

// LookupAdapter is one provider's CEP lookup. Pure HTTP mapper — no cache, no
// DB.
type LookupAdapter interface {
	Code() ProviderCode
	// Lookup resolves postalCode (8 digits, already normalized by the
	// caller) into an Address. Returns ErrNotFound when the provider is
	// reachable and confirms the postal code does not exist.
	Lookup(ctx context.Context, postalCode string) (Address, error)
}
