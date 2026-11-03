package kyc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

var acmeDraft = models.BusinessProfile{
	TaxIdentity: models.TaxIdentity{Type: models.TaxIDTypeCNPJ, Country: "BRA"}, TaxID: "12ABC34501DE35",
	LegalName: "ACME TECNOLOGIA LTDA (RECEITA)",
}

// The declaration a business application leaves: the draft's document, the
// rest as the application states it, canonical — the company phone in E.164
// for its registered country, an address's subdivision only where its country
// uses one, the activity answers that need no free text without it — and the
// owner list attested when it was sent.
func TestBusinessDeclaration(t *testing.T) {
	in := validCompany()
	in.Phone = "11987654321"
	in.Website = " https://acme.com.br "
	in.AccountPurposeOther = "ignored unless other"
	in.MoneyServicesDescription = "ignored unless it moves money"
	in.RegisteredAddress.PostalCode = " 01304-000 "
	physical := dto.AddressDTO{StreetLine1: "Calle 1", City: "Panamá", Subdivision: "8", Country: "PAN"}
	in.PhysicalAddress = &physical

	profile := businessDeclaration(in, acmeDraft, "2026-10-19T04:00:00Z")

	assert.Equal(t, acmeDraft.TaxIdentity, profile.TaxIdentity)
	assert.Equal(t, "12ABC34501DE35", profile.TaxID)
	assert.Equal(t, "ACME TECNOLOGIA LTDA", profile.LegalName, "the application names the company")
	assert.Equal(t, "+5511987654321", profile.Phone)
	assert.Equal(t, "https://acme.com.br", profile.Website)
	assert.Equal(t, "01304-000", profile.RegisteredAddress.PostalCode, "as the person form keeps one")
	require.NotNil(t, profile.PhysicalAddress)
	assert.Equal(t, "8", profile.PhysicalAddress.Subdivision)
	assert.Empty(t, profile.AccountPurposeOther)
	assert.Empty(t, profile.MoneyServicesDescription)
	assert.False(t, profile.RegulatedActivity)
	assert.Equal(t, "2026-10-19T04:00:00Z", profile.OwnershipAttestedAt)
}

// Each person is kept as the person form keeps one, their identity beside
// them; the representative is linked to the person's own customer.
func TestBusinessPeople(t *testing.T) {
	in := validCompany()
	in.AssociatedPersons[1].Phone = "(11) 92345-6789"

	people := businessPeople(in, 7)

	require.Len(t, people, 2)
	representative := people[0]
	require.NotNil(t, representative.IndividualCustomerID)
	assert.Equal(t, uint(7), *representative.IndividualCustomerID)
	assert.Equal(t, []string{"owner", "control", "signer"}, representative.Roles)
	assert.Equal(t, "60", representative.OwnershipPercent)
	assert.Equal(t, "Sócia-administradora", representative.Title)
	assert.Equal(t, "39053344705", representative.Person.TaxID)
	assert.Equal(t, models.TaxIDTypeCPF, representative.Person.TaxIDType)
	assert.Equal(t, "BRA", representative.Person.TaxIDCountry)
	assert.Equal(t, "BRA", representative.Person.Address.Country)
	assert.Nil(t, people[1].IndividualCustomerID)
	assert.Equal(t, "+5511923456789", people[1].Person.Phone)
}

// The application a provider receives: the company, and each person with the
// provider's id for them when an earlier application named them, the
// representative marked, the share as a number.
func TestBusinessApplication(t *testing.T) {
	in := validCompany()
	profile := businessDeclaration(in, acmeDraft, "2026-10-19T04:00:00Z")
	people := businessPeople(in, 7)

	app := businessApplication(profile, people, map[string]string{in.AssociatedPersons[0].LocalRef: "ap_ana"})

	assert.Equal(t, models.CustomerTypeBusiness, app.Type)
	require.NotNil(t, app.Business)
	assert.Equal(t, profile, *app.Business)
	require.Len(t, app.AssociatedPersons, 2)
	assert.Equal(t, ramp.AssociatedPerson{
		LocalRef: in.AssociatedPersons[0].LocalRef, ExternalID: "ap_ana", Representative: true,
		Roles: []string{"owner", "control", "signer"}, OwnershipPercent: 60, Title: "Sócia-administradora",
		Profile: people[0].Person,
	}, app.AssociatedPersons[0])
	assert.Empty(t, app.AssociatedPersons[1].ExternalID, "a person no application named before is new")
	assert.Equal(t, 40.0, app.AssociatedPersons[1].OwnershipPercent)
}

// The provider's ids for a business's people ride on the state of the call
// that named them, and are kept by the row; a state that names nobody — a
// refresh, which reads the customer back — leaves them as they were.
func TestApplyKycStateKeepsThePeopleTheProviderNamed(t *testing.T) {
	row := &models.CustomerProviderKyc{KycStatus: models.KycStatusSubmitting}

	applyKycState(row, &ramp.KycState{Status: models.KycStatusRequiresInfo,
		AssociatedPersonIDs: map[string]string{"ref-ana": "ap_ana"}})
	assert.Equal(t, map[string]string{"ref-ana": "ap_ana"}, row.PersonIDs())

	applyKycState(row, &ramp.KycState{Status: models.KycStatusApproved})
	assert.Equal(t, map[string]string{"ref-ana": "ap_ana"}, row.PersonIDs())
}
