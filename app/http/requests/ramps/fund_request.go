package ramps

import (
	"github.com/goravel/framework/contracts/http"
	"github.com/macrowallets/waas/app/http/requests"
)

type FundRequest struct {
	requests.Open
	Passphrase     string `json:"passphrase" form:"passphrase"`
	Code           string `json:"code" form:"code"`
	ActionRevision int    `json:"action_revision" form:"action_revision"`
}

func (*FundRequest) Rules(http.Context) map[string]string {
	return map[string]string{"passphrase": "required|min_len:12", "code": "required|len:6", "action_revision": "numeric|min:0"}
}
