package ramp

import (
	"errors"
	"fmt"
	"slices"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
)

// JurisdictionPolicy is optional, like ApplicationDocumentsRequirer: what a
// provider opens for a person, by the tax identity they declared and where
// they live (residence, ISO alpha-3 or alpha-2). An empty answer means the
// provider does not serve them.
//
// It is optional because a provider that serves everyone it supports has
// nothing to declare; only one that refuses by jurisdiction or by the kind of
// tax id needs the method, and the assertion failing is the answer "no
// restriction" (see RailsOf).
type JurisdictionPolicy interface {
	RailsFor(identity TaxIdentity, residence string) []Rail
}

// RailsOf is the policy's answer, or every rail the adapter Supports for a
// provider without one. A company is the exception: only a provider that takes
// business customers (BusinessKycAdapter) serves it, policy or not, because a
// provider that never heard of a CNPJ would otherwise open it every rail.
//
// Supports takes a whole Capability, not a rail, so the derivation asks it
// the way the stack does everywhere else: each known rail with each of
// models.RampTokens on models.ChainSolana, and a rail counts when any token
// is served. That is the faithful reading of "what this adapter can do"
// without a second list of rails to keep in step.
func RailsOf(adapter ProviderAdapter, identity TaxIdentity, residence string) []Rail {
	if _, serves := BusinessKycOf(adapter); identity.IsBusiness() && !serves {
		return nil
	}
	if policy, ok := adapter.(JurisdictionPolicy); ok {
		return policy.RailsFor(identity, residence)
	}
	var rails []Rail
	for _, rail := range AllRails() {
		for _, token := range models.RampTokens {
			if adapter.Supports(Capability{Rail: rail, Token: token, Chain: models.ChainSolana}) {
				rails = append(rails, rail)
				break
			}
		}
	}
	return rails
}

// Person is who a call is for, as a jurisdiction policy reads them: the tax
// identity they declared and where they live (ISO alpha-3). A business is a
// Person too: its company's tax identity, resident where it was incorporated
// (KYB spec §7.2).
//
// A nil *Person is nobody in particular — an individual who retained no
// profile, a request that declares no one — and every provider opens every
// rail to it, which is how every call read before eligibility was per person.
// The router and the KYC listings read the same person through
// Persons.RetainedPerson, so what a listing shows open is what the next
// operation is allowed.
type Person struct {
	Identity  TaxIdentity
	Residence string
}

// personOf is who a retained profile declares; nil for none. A profile older
// than tax_id_type is read the way it was declared (KycProfile.TaxIdentity).
func personOf(profile *Profile) *Person {
	if profile == nil {
		return nil
	}
	return &Person{Identity: profile.TaxIdentity(), Residence: profile.Address.Country}
}

// ErrBusinessPersonUnknown is a business customer nothing can say who it is
// yet: the business tables that declare a company feed BusinessPersons, and
// until one is wired a business is refused rather than read as nobody in
// particular, which every provider opens every rail to.
var ErrBusinessPersonUnknown = errors.New("ramp: who a business customer is cannot be read")

// BusinessPersons reads who a business customer is to a jurisdiction policy:
// the company it declared — its tax identity and incorporation country — or
// nil when it declared none.
type BusinessPersons interface {
	BusinessPerson(customerID uint) (*Person, error)
}

// Persons reads who a customer declared itself to be, which is what every
// rail is opened by (RailsOf).
type Persons struct {
	Customers repositories.CustomerRepository
	Profiles  repositories.CustomerIndividualProfileRepository
	// Businesses is optional; without it a business is ErrBusinessPersonUnknown.
	Businesses BusinessPersons
}

// RetainedPerson is who customerID declared: an individual's retained
// profile, nil before its first application, or a business's company. Only
// an individual has a profile, so the customer itself is read only when there
// is none.
//
// A failed read is an error, never "nobody in particular": that would open to
// a passport what only a CPF opens, and to a company what only a person is
// served.
func (p Persons) RetainedPerson(customerID uint) (*Person, error) {
	profile, err := p.Profiles.Find(customerID)
	if err != nil {
		return nil, fmt.Errorf("ramp: read the retained profile: %w", err)
	}
	if profile != nil {
		return personOf(profile), nil
	}
	customer, err := p.Customers.FindByID(customerID)
	if err != nil {
		return nil, fmt.Errorf("ramp: read customer %d: %w", customerID, err)
	}
	if customer == nil {
		return nil, fmt.Errorf("ramp: customer %d not found", customerID)
	}
	if customer.Type != models.CustomerTypeBusiness {
		return nil, nil
	}
	if p.Businesses == nil {
		return nil, ErrBusinessPersonUnknown
	}
	return p.Businesses.BusinessPerson(customerID)
}

// Opens reports whether adapter opens rail to p.
func (p *Person) Opens(adapter ProviderAdapter, rail Rail) bool {
	return p == nil || slices.Contains(RailsOf(adapter, p.Identity, p.Residence), rail)
}

// OpensAny reports whether adapter opens some rail to p: whether it serves
// them at all.
func (p *Person) OpensAny(adapter ProviderAdapter) bool {
	return p == nil || len(RailsOf(adapter, p.Identity, p.Residence)) > 0
}
