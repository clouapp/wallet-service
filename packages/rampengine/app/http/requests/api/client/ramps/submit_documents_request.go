package ramps

import (
	contractshttp "github.com/goravel/framework/contracts/http"

	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/http/requests"
)

// maxImageLen bounds one base64 data URI at roughly 5 MB of decoded image. It is
// the size limit: nothing in the app caps the body as a whole (the reverse proxy
// does), and `start_with:data:image/` only bounds a shape trivial to forge.
const maxImageLen = "max_len:7000000"

// SubmitDocumentsRequest validates POST /api/kyc/{provider}/documents.
//
// Images arrive as data URIs. They are the reason this endpoint is separate
// from /applications: the profile stays a small JSON body, and the bytes are
// bounded here, where the size limit belongs.
//
// image_front is not `required`, and neither is document_type: a retry that
// owes only a proof of address — or only a selfie — sends that alone, and
// document_type describes an identity document those calls do not carry.
// "At least one file per call" is checked in the KYC service (documentsFrom)
// rather than here, because required_with/required_without misfire on
// struct-bound requests — the struct's zero value counts as present.
type SubmitDocumentsRequest struct {
	requests.Open
	Provider string `form:"provider" json:"provider"`
	DocumentFields
}

// DocumentFields is the set of files both document endpoints carry; the
// fan-out embeds it too, so the two cannot drift apart.
type DocumentFields struct {
	// DocumentType is national_id, drivers_license, passport or
	// state_or_provincial_id (a US state ID).
	DocumentType string `form:"document_type" json:"document_type"`
	// IssuingCountry is the ISO alpha-3 country that issued the document, in
	// any case (installed builds send it lower-case: "bra").
	IssuingCountry string `form:"issuing_country" json:"issuing_country"`
	ImageFront     string `form:"image_front" json:"image_front"`
	ImageBack      string `form:"image_back" json:"image_back"`
	Selfie         string `form:"selfie" json:"selfie"`
	// ProofOfAddress is a utility bill or bank letter as an image; the app
	// rasterises a PDF's first page before sending. Optional here and paired
	// with image_front in the KYC service: at least one file per call.
	ProofOfAddress string `form:"proof_of_address" json:"proof_of_address"`
	// BankStatement is sent as the PDF itself, or as a photo: a statement runs
	// to several pages, and rasterising the first would keep only the header.
	BankStatement string `form:"bank_statement" json:"bank_statement"`
}

// Input copies the files into the service's input as the client sent them;
// building the documents, and refusing a body that cannot make them, is the
// service's job.
func (f DocumentFields) Input() dto.DocumentsDTO {
	return dto.DocumentsDTO{
		DocumentType:   f.DocumentType,
		IssuingCountry: f.IssuingCountry,
		ImageFront:     f.ImageFront,
		ImageBack:      f.ImageBack,
		Selfie:         f.Selfie,
		ProofOfAddress: f.ProofOfAddress,
		BankStatement:  f.BankStatement,
	}
}

func (r *SubmitDocumentsRequest) Rules(ctx contractshttp.Context) map[string]string {
	return map[string]string{
		"provider":         ProviderRule,
		"document_type":    "in:national_id,drivers_license,passport,state_or_provincial_id",
		"issuing_country":  "len:3|kyc_country",
		"image_front":      "start_with:data:image/|" + maxImageLen,
		"image_back":       "start_with:data:image/|" + maxImageLen,
		"selfie":           "start_with:data:image/|" + maxImageLen,
		"proof_of_address": "start_with:data:image/|" + maxImageLen,
		"bank_statement":   "kyc_document_file|" + maxImageLen,
	}
}
