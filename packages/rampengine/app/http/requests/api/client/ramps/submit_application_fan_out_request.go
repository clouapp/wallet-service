package ramps

import (
	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/packages/rampengine/app/http/requests"
)

// SubmitApplicationFanOutRequest validates POST /api/kyc/applications.
//
// It carries SubmitApplicationRequest's profile, minus provider and
// signed_agreement_id: the client sends one application, and which providers
// receive it is the service's decision — the whole point of a multi-provider
// stack is that the app never names one. It embeds ApplicationFields rather
// than SubmitApplicationRequest so a field this endpoint does not accept cannot
// still surface in its generated schema.
//
// signed_agreement_id is absent for a stronger reason than provider is. It is
// issued by one provider and means nothing at another, and this body reaches
// every provider — one field could only ever carry one of them. It is recorded
// per provider through POST /api/kyc/tos-acceptances, and SubmitApplication
// reads each provider's own from its own row.
type SubmitApplicationFanOutRequest struct {
	requests.Open
	ApplicationFields
}

// Rules reuses SubmitApplicationRequest's table for every field the two
// endpoints share, minus the one this endpoint does not take. Reusing the
// table rather than retyping twenty-four rule strings is what keeps the two
// from drifting apart the next time one of them changes.
func (r *SubmitApplicationFanOutRequest) Rules(ctx contractshttp.Context) map[string]string {
	rules := (&SubmitApplicationRequest{}).Rules(ctx)
	delete(rules, "provider")
	delete(rules, "signed_agreement_id")
	return rules
}
