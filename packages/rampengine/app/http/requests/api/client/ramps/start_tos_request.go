package ramps

import (
	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/packages/rampengine/app/http/requests"
)

// StartTosRequest validates POST /api/kyc/{provider}/tos-sessions.
type StartTosRequest struct {
	requests.Open
	Provider    string `form:"provider" json:"provider"`
	RedirectURI string `form:"redirect_uri" json:"redirect_uri"`
}

func (r *StartTosRequest) Rules(ctx contractshttp.Context) map[string]string {
	return map[string]string{
		"provider": ProviderRule,
		// The app hands back a deep link, so the scheme is not necessarily http.
		"redirect_uri": "max_len:2048",
	}
}
