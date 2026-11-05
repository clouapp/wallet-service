package kyc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

func storedPeople() []models.BusinessPerson {
	return businessPeople(validCompany(), 7)
}

// The documents call becomes the company's documents by purpose and each
// person's: their tax id restated beside the photographed document, as the
// provider keeps it (the leaf's probe), placed by the provider's id for them.
func TestBusinessDocuments(t *testing.T) {
	people := storedPeople()
	in := dto.BusinessDocumentsDTO{
		CompanyDocuments: []dto.CompanyDocumentDTO{{Purpose: "formation", File: "data:application/pdf;base64,JVBE"}},
		PersonDocuments: []dto.PersonDocumentDTO{{LocalRef: people[1].LocalRef, DocumentType: "passport",
			IssuingCountry: "arg", ImageFront: "data:image/jpeg;base64,AAAA"}},
	}

	docs, err := businessDocuments(in, people, map[string]string{people[1].LocalRef: "ap_bruno"})

	require.NoError(t, err)
	assert.Equal(t, []ramp.CompanyDocument{{Purpose: "formation", File: "data:application/pdf;base64,JVBE"}}, docs.CompanyDocuments)
	assert.Equal(t, []ramp.PersonDocuments{{LocalRef: people[1].LocalRef, ExternalID: "ap_bruno", Documents: []ramp.IdentifyingDocument{
		{Type: models.TaxIDTypeCPF, IssuingCountry: "BRA", Number: "52998224725"},
		{Type: "passport", IssuingCountry: "ARG", ImageFront: "data:image/jpeg;base64,AAAA"},
	}}}, docs.PersonDocuments)
}

func TestBusinessDocuments_Refusals(t *testing.T) {
	people := storedPeople()
	file := "data:application/pdf;base64,JVBE"
	for _, tc := range []struct {
		name string
		in   dto.BusinessDocumentsDTO
		want error
	}{
		{"nothing at all", dto.BusinessDocumentsDTO{}, ErrDocumentsRequired},
		{"an image with no type", dto.BusinessDocumentsDTO{PersonDocuments: []dto.PersonDocumentDTO{
			{LocalRef: people[0].LocalRef, ImageFront: "data:image/jpeg;base64,AAAA"}}}, ErrDocumentTypeRequired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := businessDocuments(tc.in, people, nil)
			assert.ErrorIs(t, err, tc.want)
		})
	}

	_, err := businessDocuments(dto.BusinessDocumentsDTO{
		CompanyDocuments: []dto.CompanyDocumentDTO{{Purpose: "formation", File: file}, {Purpose: "formation", File: file}},
		PersonDocuments: []dto.PersonDocumentDTO{{LocalRef: "nobody", DocumentType: "passport",
			ImageFront: "data:image/jpeg;base64,AAAA"}},
	}, people, nil)
	var refusal *FieldRefusal
	require.ErrorAs(t, err, &refusal)
	assert.Equal(t, RefusalValidationFailed, refusal.Code)
	assert.Contains(t, refusal.Fields["company_documents"], "distinct")
	assert.Contains(t, refusal.Fields["person_identity_documents.0.local_ref"], "exists")
}
