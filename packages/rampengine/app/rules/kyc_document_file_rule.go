package rules

import (
	"context"
	"strings"

	"github.com/goravel/framework/contracts/validation"
)

// KycDocumentFileRule accepts a supporting document as a base64 data URI that
// is either a PDF or an image. A bank statement arrives both ways: downloaded
// from the bank's app as a PDF, or photographed.
//
// It exists because start_with takes a single prefix, which suits the image
// fields and cannot say "this or that". Only the envelope is checked here; the
// adapter that uploads the file decodes it and refuses a mime its provider
// does not take.
type KycDocumentFileRule struct{}

func (r *KycDocumentFileRule) Signature() string { return "kyc_document_file" }

func (r *KycDocumentFileRule) Passes(_ context.Context, _ validation.Data, val any, _ ...any) bool {
	value, ok := val.(string)
	if !ok {
		return false
	}
	header, _, found := strings.Cut(value, ",")
	if !found || !strings.HasPrefix(header, "data:") {
		return false
	}
	mime := strings.ToLower(strings.TrimPrefix(header, "data:"))
	return strings.HasPrefix(mime, "application/pdf;") || strings.HasPrefix(mime, "image/")
}

func (r *KycDocumentFileRule) Message(_ context.Context) string {
	return "the :attribute must be a PDF or an image, as a base64 data URI"
}
