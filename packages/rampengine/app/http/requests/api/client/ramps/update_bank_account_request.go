package ramps

import (
	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/packages/rampengine/app/http/requests"
)

// UpdateBankAccountRequest validates PUT /api/bank-accounts/{id}. Only the
// label is editable: other numbers are another account, deleted and saved
// anew under the step-up.
type UpdateBankAccountRequest struct {
	requests.Open
	Label string `form:"label" json:"label"`
} //	@name	ClientUpdateBankAccountRequest

func (r *UpdateBankAccountRequest) Rules(ctx contractshttp.Context) map[string]string {
	return map[string]string{"label": "required|max_len:64"}
}
