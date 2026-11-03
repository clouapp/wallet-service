package ramps

import (
	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/packages/rampengine/app/http/requests"
)

// sourceCurrencyRule is the closed set of currencies a standing account
// collects. Optional: an absent currency is brl, what every installed build
// means. It encodes one fact of the domain shared by the three account
// requests (CLAUDE.md, Form Requests).
const sourceCurrencyRule = "in:brl,usd"

// ShowVirtualAccountRequest validates GET /api/onramps/account. The query
// names which account to read: brl when absent, or usd.
type ShowVirtualAccountRequest struct {
	requests.Open
	SourceCurrency string `form:"source_currency" json:"source_currency"`
}

func (r *ShowVirtualAccountRequest) Rules(ctx contractshttp.Context) map[string]string {
	return map[string]string{"source_currency": sourceCurrencyRule}
}
