package customers

import (
	"fmt"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
)

// CompanyPersons reads a business customer as the jurisdiction policies read a
// person (ramp.BusinessPersons): the tax identity its draft declared and its
// country of incorporation, which is where the company resides to every
// provider (spec §7.2) — the registered address of its application is held to
// that country. The draft's, which no application changes.
type CompanyPersons struct {
	profiles repositories.CustomerBusinessProfileRepository
}

// NewCompanyPersons wires the reader.
func NewCompanyPersons(profiles repositories.CustomerBusinessProfileRepository) CompanyPersons {
	return CompanyPersons{profiles: profiles}
}

// BusinessPerson is the company customerID declared, or nil when it declared
// none. A declaration that cannot be read is an error.
func (c CompanyPersons) BusinessPerson(customerID uint) (*ramp.Person, error) {
	profiles, err := c.profiles.FindByCustomers([]uint{customerID})
	if err != nil {
		return nil, fmt.Errorf("customers: read the company of customer %d: %w", customerID, err)
	}
	profile, ok := profiles[customerID]
	if !ok {
		return nil, nil
	}
	return &ramp.Person{Identity: profile.TaxIdentity, Residence: profile.TaxIdentity.Country}, nil
}
