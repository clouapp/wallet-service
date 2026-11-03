package kyc

import (
	"strconv"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// businessDocuments is a KYB documents call as every adapter reads it (API
// contract §6.1): the company's documents by purpose, and each person's
// identity document with their tax id restated beside it — the provider keeps
// a person's tax id only when it is sent with the image — placed by the
// provider's id for them (externalIDs, by local_ref, from the application's
// state).
//
// It refuses, before anything is staged, what spans the entries: no file at
// all (ErrDocumentsRequired), an image no document type names
// (ErrDocumentTypeRequired), and in the validation shape a purpose sent twice
// (company_documents → distinct) and a person the last application did not
// name (person_identity_documents.<i>.local_ref → exists).
func businessDocuments(in dto.BusinessDocumentsDTO, people []models.BusinessPerson, externalIDs map[string]string) (ramp.Documents, error) {
	if len(in.CompanyDocuments) == 0 && len(in.PersonDocuments) == 0 {
		return ramp.Documents{}, ErrDocumentsRequired
	}
	refusal := &FieldRefusal{Code: RefusalValidationFailed, Fields: map[string]map[string]string{}}
	var docs ramp.Documents
	purposes := map[string]bool{}
	for _, document := range in.CompanyDocuments {
		if purposes[document.Purpose] {
			refusal.add("company_documents", "distinct", "one document per purpose")
		}
		purposes[document.Purpose] = true
		docs.CompanyDocuments = append(docs.CompanyDocuments, ramp.CompanyDocument{Purpose: document.Purpose, File: document.File})
	}
	byRef := make(map[string]models.BusinessPerson, len(people))
	for _, person := range people {
		byRef[person.LocalRef] = person
	}
	for i, document := range in.PersonDocuments {
		if document.ImageFront != "" && document.DocumentType == "" {
			return ramp.Documents{}, ErrDocumentTypeRequired
		}
		person, ok := byRef[document.LocalRef]
		if !ok {
			refusal.add("person_identity_documents."+strconv.Itoa(i)+".local_ref", "exists",
				"a person the application names")
			continue
		}
		docs.PersonDocuments = append(docs.PersonDocuments, personDocuments(person, document, externalIDs[person.LocalRef]))
	}
	if len(refusal.Fields) > 0 {
		return ramp.Documents{}, refusal
	}
	return docs, nil
}

// personDocuments is one person's documents: their declared tax id, then the
// photographed document.
func personDocuments(person models.BusinessPerson, document dto.PersonDocumentDTO, externalID string) ramp.PersonDocuments {
	return ramp.PersonDocuments{
		LocalRef: person.LocalRef, ExternalID: externalID,
		Documents: []ramp.IdentifyingDocument{
			taxIDDocument(person.Person.TaxID, person.Person.TaxIdentity()),
			{Type: document.DocumentType, IssuingCountry: issuingCountry(document.IssuingCountry),
				ImageFront: document.ImageFront, ImageBack: document.ImageBack},
		},
	}
}
