package ramps

import (
	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/packages/rampengine/app/http/requests"
)

// RefreshFundingActionRequest validates POST /api/offramps/{id}/funding-action/refresh.
// The endpoint carries no body, so its controller validates it with
// requests.ValidatePath. The {id} is the order's opaque external id: one that
// cannot exist answers 404 from the lookup.
type RefreshFundingActionRequest struct {
	requests.Open
	ID string `form:"id" json:"id"`
}

func (r *RefreshFundingActionRequest) Rules(contractshttp.Context) map[string]string {
	return map[string]string{"id": "required"}
}
