// Package companyregistry defines the provider-agnostic contract for looking a
// Brazilian company up by its CNPJ in the Receita's registry (KYB spec §7.1).
//
// It is app/adapters/postal's design for another registry: an adapter is a
// pure HTTP mapper from a CNPJ to one provider's call and back into the
// canonical Company, and never touches the database or the cache, which are
// the service layer's.
package companyregistry

import (
	"context"
	"errors"
)

type ProviderCode = string

const ProviderBrasilApi ProviderCode = "brasilapi"

// The registration statuses (situação cadastral) the Receita gives a company.
const (
	StatusActive    = "active"    // ATIVA
	StatusSuspended = "suspended" // SUSPENSA
	StatusUnfit     = "unfit"     // INAPTA
	StatusClosed    = "closed"    // BAIXADA
	StatusVoid      = "void"      // NULA
)

// Company is what the registry says of a company, independent of which
// provider answered. Everything in it is a suggestion the user confirms.
type Company struct {
	// TaxID is the CNPJ in its canonical spelling (brazil.CanonicalDocument).
	TaxID     string
	LegalName string // razão social
	TradeName string // nome fantasia; empty when the company has none
	// LegalNature is the Concla code (four digits, "2062") and its
	// description.
	LegalNature LegalNature
	// MEI reports whether the company is enrolled as a microempreendedor
	// individual.
	MEI bool
	// Partners is how many partners the registry lists. Who they are is
	// personal data the KYB never takes from the registry.
	Partners int
	// IncorporationDate is when the company opened, YYYY-MM-DD.
	IncorporationDate string
	// Status is one of the Status* constants, or empty for a status nobody
	// has mapped.
	Status  string
	Address Address
}

// Active reports whether the Receita holds the company ATIVA.
func (c Company) Active() bool { return c.Status == StatusActive }

// LegalNature is a company's natureza jurídica.
type LegalNature struct {
	Code        string
	Description string
}

// Address is the company's registered address.
type Address struct {
	StreetLine1  string // street type and name ("AVENIDA REPUBLICA DO CHILE")
	Number       string
	StreetLine2  string // complemento
	Neighborhood string
	City         string
	Subdivision  string // UF, 2 letters
	PostalCode   string // 8 digits
	Country      string // always "BRA"
}

// Sentinel errors for the company lookup stack.
var (
	// ErrNotFound means a provider answered and confirmed the registry holds
	// no such company. The router treats it as definitive and does not ask
	// the next provider.
	ErrNotFound = errors.New("company registry: company not found")
	// ErrProviderNotImplemented is a code with no registered adapter.
	ErrProviderNotImplemented = errors.New("company registry: provider not implemented")
	// ErrNoProviderAvailable means every configured provider was skipped
	// (disabled or unregistered).
	ErrNoProviderAvailable = errors.New("company registry: no provider available")
	// ErrProviderFailed marks a lookup the provider did not answer usefully:
	// unreachable, a status other than a definitive not-found, or a body this
	// side could not read. It is what the HTTP layer answers 502 for.
	ErrProviderFailed = errors.New("company registry: provider call failed")
)

// LookupAdapter is one provider's CNPJ lookup. Pure HTTP mapper — no cache,
// no DB.
type LookupAdapter interface {
	Code() ProviderCode
	// Lookup resolves cnpj (canonical, already validated by the caller) into
	// a Company. Returns ErrNotFound when the provider is reachable and
	// confirms the registry holds no such company.
	Lookup(ctx context.Context, cnpj string) (Company, error)
}
