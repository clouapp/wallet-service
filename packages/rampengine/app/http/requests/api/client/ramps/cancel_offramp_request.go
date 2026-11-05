package ramps

import (
	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/packages/rampengine/app/http/requests"
)

// CancelOfframpRequest validates POST /api/offramps/{id}/cancel. The endpoint
// carries no body, so its controller validates it with requests.ValidatePath.
type CancelOfframpRequest struct {
	requests.Open
	ID string `form:"id" json:"id"`
}

func (r *CancelOfframpRequest) Rules(ctx contractshttp.Context) map[string]string {
	return map[string]string{"id": "required|uuid"}
}
