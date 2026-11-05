package ramps

import (
	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/packages/rampengine/app/http/requests"
)

// ShowOrderRequest validates GET /api/onramps/{id} and /api/offramps/{id}.
// The id is the transaction's external_id; internal ids never leave the server.
type ShowOrderRequest struct {
	requests.Open
	ID string `form:"id" json:"id"`
}

func (r *ShowOrderRequest) Rules(ctx contractshttp.Context) map[string]string {
	return map[string]string{"id": "required|uuid"}
}
