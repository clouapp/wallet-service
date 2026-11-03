package ramps

import (
	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/packages/rampengine/app/http/requests"
)

// ListOrdersRequest validates the list window of GET /api/onramps and
// GET /api/offramps. requests.Paging clamps the values, so the rules
// only refuse what cannot be read as a number.
type ListOrdersRequest struct {
	requests.Open
	Page    int `form:"page" json:"page"`
	PerPage int `form:"per_page" json:"per_page"`
} //	@name	ClientListOrdersRequest

func (r *ListOrdersRequest) Rules(ctx contractshttp.Context) map[string]string {
	return map[string]string{
		"page":     "numeric",
		"per_page": "numeric",
	}
}
