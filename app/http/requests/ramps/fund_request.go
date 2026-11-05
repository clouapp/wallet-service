package ramps

import (
	"github.com/goravel/framework/contracts/http"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
)

type FundRequest struct {
	requests.Open
	Passphrase     string `json:"passphrase" form:"passphrase"`
	Code           string `json:"code" form:"code"`
	ActionRevision int    `json:"action_revision" form:"action_revision"`
}

func (*FundRequest) Rules(ctx http.Context) map[string]string {
	rules := map[string]string{"passphrase": "required|min_len:12", "action_revision": "numeric|min:0"}
	if token, ok := requestctx.APIToken(ctx); !ok || token == nil {
		rules["code"] = "required|len:6"
	}
	return rules
}
