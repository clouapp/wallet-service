package ramps

import (
	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/packages/rampengine/app/http/requests"
)

// RequestEndorsementsRequest validates POST /api/kyc/{provider}/endorsements.
type RequestEndorsementsRequest struct {
	requests.Open
	Provider     string   `form:"provider" json:"provider"`
	Endorsements []string `form:"endorsements" json:"endorsements"`
}

func (r *RequestEndorsementsRequest) Rules(ctx contractshttp.Context) map[string]string {
	return map[string]string{
		"provider":     ProviderRule,
		"endorsements": "required|array",
	}
}
