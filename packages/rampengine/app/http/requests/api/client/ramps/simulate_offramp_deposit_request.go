package ramps

import (
	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/packages/rampengine/app/http/requests"
)

// SimulateOfframpDepositRequest validates POST /api/offramps/{id}/simulate-deposit.
// The endpoint carries no body, so its controller validates it with
// requests.ValidatePath.
type SimulateOfframpDepositRequest struct {
	requests.Open
	ID string `form:"id" json:"id"`
}

func (r *SimulateOfframpDepositRequest) Rules(ctx contractshttp.Context) map[string]string {
	return map[string]string{"id": "required|uuid"}
}
