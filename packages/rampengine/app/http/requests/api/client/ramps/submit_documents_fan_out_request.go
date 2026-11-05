package ramps

import (
	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/packages/rampengine/app/http/requests"
)

// SubmitDocumentsFanOutRequest validates POST /api/kyc/documents.
//
// It carries SubmitDocumentsRequest's files, minus provider — same reasoning
// as SubmitApplicationFanOutRequest: the client sends one set of documents,
// and which providers receive it is the service's decision.
type SubmitDocumentsFanOutRequest struct {
	requests.Open
	DocumentFields
}

// Rules reuses SubmitDocumentsRequest's table for every field the two
// endpoints share, minus the one this endpoint does not take. Reusing the
// table rather than retyping five rule strings is what keeps the two from
// drifting apart the next time one of them changes.
func (r *SubmitDocumentsFanOutRequest) Rules(ctx contractshttp.Context) map[string]string {
	rules := (&SubmitDocumentsRequest{}).Rules(ctx)
	delete(rules, "provider")
	return rules
}
