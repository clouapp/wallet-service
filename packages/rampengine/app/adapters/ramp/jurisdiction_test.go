package ramp_test

import (
	"errors"
	"testing"

	"github.com/goravel/framework/database/orm"
	"github.com/stretchr/testify/assert"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
)

// supportsOnly is a provider with no jurisdiction policy that serves one
// rail, for one token.
type supportsOnly struct {
	ramp.ProviderAdapter
	rail  ramp.Rail
	token string
}

func (s supportsOnly) Supports(c ramp.Capability) bool {
	return c.Rail == s.rail && c.Token == s.token && c.Chain == models.ChainSolana
}

type withPolicy struct {
	supportsOnly
	rails []ramp.Rail
}

func (w withPolicy) RailsFor(ramp.TaxIdentity, string) []ramp.Rail { return w.rails }

func TestRailsOf_WithoutAPolicyIsEveryRailTheAdapterSupports(t *testing.T) {
	adapter := supportsOnly{rail: ramp.RailPixPayout, token: models.TargetTokenUSDT}

	got := ramp.RailsOf(adapter, ramp.TaxIdentity{Type: models.TaxIDTypeCPF, Country: "BRA"}, "BRA")

	assert.Equal(t, []ramp.Rail{ramp.RailPixPayout}, got)
}

func TestRailsOf_ASupportingNothingAdapterServesNobody(t *testing.T) {
	got := ramp.RailsOf(supportsOnly{}, ramp.TaxIdentity{Type: models.TaxIDTypeCPF, Country: "BRA"}, "BRA")

	assert.Empty(t, got)
}

func TestRailsOf_ThePolicyAnswersInsteadOfSupports(t *testing.T) {
	adapter := withPolicy{
		supportsOnly: supportsOnly{rail: ramp.RailPixPayin, token: models.TargetTokenUSDC},
		rails:        []ramp.Rail{ramp.RailPixPayout},
	}

	got := ramp.RailsOf(adapter, ramp.TaxIdentity{Type: models.TaxIDTypePassport, Country: "ARG"}, "ARG")

	assert.Equal(t, []ramp.Rail{ramp.RailPixPayout}, got)
}

// individual is customer id as the backfill and the sign-up leave every
// account's: an individual whose id is the user's.
func individual(id uint) *models.Customer {
	return &models.Customer{Model: orm.Model{ID: id}, Type: models.CustomerTypeIndividual, Status: models.CustomerStatusActive}
}

func business(id uint) *models.Customer {
	return &models.Customer{Model: orm.Model{ID: id}, Type: models.CustomerTypeBusiness, Status: models.CustomerStatusDraft}
}

// The retained profile is read the way it was declared: a profile retained
// before tax_id_type existed (an installed build: no type, BRA) is a CPF
// holder living in Brazil. Only an individual has one, so the customer is not
// read for it.
func TestPersons_ReadsTheDeclarationAsItWasMade(t *testing.T) {
	for name, tc := range map[string]struct {
		profile *ramp.Profile
		want    *ramp.Person
	}{
		"an installed build": {&ramp.Profile{TaxID: "39053344705", Address: ramp.Address{Country: "BRA"}},
			&ramp.Person{Identity: ramp.TaxIdentity{Type: models.TaxIDTypeCPF, Country: "BRA"}, Residence: "BRA"}},
		"a cpf abroad": {&ramp.Profile{TaxID: "39053344705", TaxIDType: models.TaxIDTypeCPF,
			TaxIDCountry: "BRA", Address: ramp.Address{Country: "GBR"}},
			&ramp.Person{Identity: ramp.TaxIdentity{Type: models.TaxIDTypeCPF, Country: "BRA"}, Residence: "GBR"}},
	} {
		t.Run(name, func(t *testing.T) {
			profiles := mocks.NewMockCustomerIndividualProfileRepository(t)
			profiles.EXPECT().Find(uint(7)).Return(tc.profile, nil).Once()
			persons := ramp.Persons{Customers: mocks.NewMockCustomerRepository(t), Profiles: profiles}

			who, err := persons.RetainedPerson(7)

			assert.NoError(t, err)
			assert.Equal(t, tc.want, who)
		})
	}
}

// A profile that cannot be read is a failure, never "nobody in particular":
// that would open to a passport what only a CPF opens.
func TestPersons_AProfileThatCannotBeReadFailsClosed(t *testing.T) {
	broken := errors.New("customer individual profile repo: connection reset")
	profiles := mocks.NewMockCustomerIndividualProfileRepository(t)
	profiles.EXPECT().Find(uint(7)).Return(nil, broken).Once()
	persons := ramp.Persons{Customers: mocks.NewMockCustomerRepository(t), Profiles: profiles}

	who, err := persons.RetainedPerson(7)

	assert.ErrorIs(t, err, broken)
	assert.Nil(t, who)
}

// An individual who has not applied yet declared nobody in particular.
func TestPersons_AnIndividualWithoutAProfileIsNobodyInParticular(t *testing.T) {
	profiles := mocks.NewMockCustomerIndividualProfileRepository(t)
	profiles.EXPECT().Find(uint(7)).Return(nil, nil).Once()
	customers := mocks.NewMockCustomerRepository(t)
	customers.EXPECT().FindByID(uint(7)).Return(individual(7), nil).Once()

	who, err := ramp.Persons{Customers: customers, Profiles: profiles}.RetainedPerson(7)

	assert.NoError(t, err)
	assert.Nil(t, who)
}

// A business is the company it declared (KYB spec §7.2), read through the seam
// the business tables fill.
func TestPersons_ABusinessIsTheCompanyItDeclared(t *testing.T) {
	const id = 1_000_000_000_000
	profiles := mocks.NewMockCustomerIndividualProfileRepository(t)
	profiles.EXPECT().Find(uint(id)).Return(nil, nil).Once()
	customers := mocks.NewMockCustomerRepository(t)
	customers.EXPECT().FindByID(uint(id)).Return(business(id), nil).Once()
	company := &ramp.Person{Identity: ramp.TaxIdentity{Type: models.TaxIDTypeCNPJ, Country: "BRA"}, Residence: "BRA"}
	businesses := mocks.NewMockBusinessPersons(t)
	businesses.EXPECT().BusinessPerson(uint(id)).Return(company, nil).Once()

	who, err := ramp.Persons{Customers: customers, Profiles: profiles, Businesses: businesses}.RetainedPerson(id)

	assert.NoError(t, err)
	assert.Equal(t, company, who)
}

// Until the business tables say who a business is, it is refused rather than
// read as nobody in particular, which every provider opens every rail to —
// Ozav included, which serves no company.
func TestPersons_ABusinessNothingReadsFailsClosed(t *testing.T) {
	const id = 1_000_000_000_000
	profiles := mocks.NewMockCustomerIndividualProfileRepository(t)
	profiles.EXPECT().Find(uint(id)).Return(nil, nil).Once()
	customers := mocks.NewMockCustomerRepository(t)
	customers.EXPECT().FindByID(uint(id)).Return(business(id), nil).Once()

	who, err := ramp.Persons{Customers: customers, Profiles: profiles}.RetainedPerson(id)

	assert.ErrorIs(t, err, ramp.ErrBusinessPersonUnknown)
	assert.Nil(t, who)
}

// A customer that does not exist names nobody: it is an error, not nobody in
// particular.
func TestPersons_ACustomerThatDoesNotExistFailsClosed(t *testing.T) {
	profiles := mocks.NewMockCustomerIndividualProfileRepository(t)
	profiles.EXPECT().Find(uint(8)).Return(nil, nil).Once()
	customers := mocks.NewMockCustomerRepository(t)
	customers.EXPECT().FindByID(uint(8)).Return(nil, nil).Once()

	who, err := ramp.Persons{Customers: customers, Profiles: profiles}.RetainedPerson(8)

	assert.Error(t, err)
	assert.Nil(t, who)
}

// What a provider opens to a person is its policy's answer; nobody in
// particular is refused nothing.
func TestPerson_OpensWhatThePolicyOpens(t *testing.T) {
	adapter := withPolicy{rails: []ramp.Rail{ramp.RailPixPayout}}
	passport := &ramp.Person{Identity: ramp.TaxIdentity{Type: models.TaxIDTypePassport, Country: "ARG"}, Residence: "ARG"}

	assert.True(t, passport.Opens(adapter, ramp.RailPixPayout))
	assert.False(t, passport.Opens(adapter, ramp.RailPixPayin))
	assert.True(t, passport.OpensAny(adapter))
	assert.False(t, passport.OpensAny(withPolicy{}), "a policy that opens nothing serves nobody")

	var nobody *ramp.Person
	assert.True(t, nobody.Opens(withPolicy{}, ramp.RailPixPayin))
	assert.True(t, nobody.OpensAny(withPolicy{}))
}

// Without a policy, a USD rail the adapter supports counts like a Pix one.
func TestRailsOf_WithoutAPolicyCountsAUSDRail(t *testing.T) {
	adapter := supportsOnly{rail: ramp.RailUSDACHPayout, token: models.TargetTokenUSDC}

	got := ramp.RailsOf(adapter, ramp.TaxIdentity{Type: models.TaxIDTypeCPF, Country: "BRA"}, "BRA")

	assert.Equal(t, []ramp.Rail{ramp.RailUSDACHPayout}, got)
}
