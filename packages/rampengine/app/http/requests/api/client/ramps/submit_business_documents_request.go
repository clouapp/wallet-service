package ramps

import (
	"strconv"

	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/http/requests"
	kycsvc "github.com/macrowallets/waas/packages/rampengine/app/services/client/kyc"
)

// SubmitBusinessDocumentsRequest validates POST /api/kyc/{provider}/documents
// in a business's scope: the KYB documents (API contract §6.1), for one
// provider.
type SubmitBusinessDocumentsRequest struct {
	requests.Open
	Provider string `form:"provider" json:"provider"`
	BusinessDocumentFields
}

func (r *SubmitBusinessDocumentsRequest) Rules(ctx contractshttp.Context) map[string]string {
	rules := businessDocumentRules(ctx)
	rules["provider"] = ProviderRule
	return rules
}

// SubmitBusinessDocumentsFanOutRequest validates POST /api/kyc/documents in a
// business's scope: the documents, which reach every provider that took the
// application.
type SubmitBusinessDocumentsFanOutRequest struct {
	requests.Open
	BusinessDocumentFields
}

func (r *SubmitBusinessDocumentsFanOutRequest) Rules(ctx contractshttp.Context) map[string]string {
	return businessDocumentRules(ctx)
}

// BusinessDocumentFields is one documents call of a KYB application: the
// company's documents by purpose and each person's identity document, sent
// together right after the application. No selfie: Bridge never asked one of
// a partner (spec §13).
type BusinessDocumentFields struct {
	CompanyDocuments        []CompanyDocumentInput `form:"company_documents" json:"company_documents"`
	PersonIdentityDocuments []PersonDocumentInput  `form:"person_identity_documents" json:"person_identity_documents"`
}

// CompanyDocumentInput is one of the company's documents.
type CompanyDocumentInput struct {
	Purpose string `form:"purpose" json:"purpose"`
	File    string `form:"file" json:"file"`
}

// PersonDocumentInput is one person's identity document, the person named by
// the local_ref the application gave them.
type PersonDocumentInput struct {
	LocalRef       string `form:"local_ref" json:"local_ref"`
	DocumentType   string `form:"document_type" json:"document_type"`
	IssuingCountry string `form:"issuing_country" json:"issuing_country"`
	ImageFront     string `form:"image_front" json:"image_front"`
	ImageBack      string `form:"image_back" json:"image_back"`
}

// Input is the documents as the service takes them.
func (f BusinessDocumentFields) Input() dto.BusinessDocumentsDTO {
	var in dto.BusinessDocumentsDTO
	for _, document := range f.CompanyDocuments {
		in.CompanyDocuments = append(in.CompanyDocuments, dto.CompanyDocumentDTO{Purpose: document.Purpose, File: document.File})
	}
	for _, document := range f.PersonIdentityDocuments {
		in.PersonDocuments = append(in.PersonDocuments, dto.PersonDocumentDTO{
			LocalRef: document.LocalRef, DocumentType: document.DocumentType, IssuingCountry: document.IssuingCountry,
			ImageFront: document.ImageFront, ImageBack: document.ImageBack,
		})
	}
	return in
}

// businessDocumentRules holds each entry to its rules under its own index,
// the key the app maps back to the file it picked. What spans entries — a
// purpose twice, a person the application does not name, nothing at all —
// is the service's.
func businessDocumentRules(ctx contractshttp.Context) map[string]string {
	body := map[string]any{}
	if ctx != nil {
		body = ctx.Request().All()
	}
	table := map[string]string{}
	companyDocuments, _ := body["company_documents"].([]any)
	for i := range companyDocuments {
		scope := "company_documents." + strconv.Itoa(i)
		table[scope+".purpose"] = "required|" + kycsvc.RuleIn(ramp.CompanyDocumentPurposes())
		table[scope+".file"] = "required|kyc_document_file|" + maxImageLen
	}
	personDocuments, _ := body["person_identity_documents"].([]any)
	for i := range personDocuments {
		scope := "person_identity_documents." + strconv.Itoa(i)
		table[scope+".local_ref"] = "required"
		table[scope+".document_type"] = "in:national_id,drivers_license,passport,state_or_provincial_id"
		table[scope+".issuing_country"] = "len:3|kyc_country"
		table[scope+".image_front"] = "start_with:data:image/|" + maxImageLen
		table[scope+".image_back"] = "start_with:data:image/|" + maxImageLen
	}
	return table
}
