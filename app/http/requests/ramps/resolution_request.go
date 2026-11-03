package ramps

import (
	"github.com/goravel/framework/contracts/http"
	"github.com/macrowallets/waas/app/http/requests"
)

type ResolutionRequest struct {
	requests.Open
	Kind       string `json:"kind" form:"kind"`
	ProviderID string `json:"provider_id" form:"provider_id"`
	Code       string `json:"code" form:"code"`
	Reference  string `json:"reference" form:"reference"`
	Note       string `json:"note" form:"note"`
	Abandon    bool   `json:"abandon" form:"abandon"`
	NotCreated bool   `json:"not_created" form:"not_created"`
}

func (*ResolutionRequest) Rules(http.Context) map[string]string {
	return map[string]string{"kind": "required|in:kyc_intent,finalization,support_ticket", "provider_id": "max_len:128", "code": "required|len:6", "reference": "max_len:64", "note": "max_len:500", "abandon": "bool", "not_created": "bool"}
}
