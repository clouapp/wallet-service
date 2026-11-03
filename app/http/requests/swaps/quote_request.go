package swaps

import (
	"github.com/goravel/framework/contracts/http"
	"github.com/macrowallets/waas/app/http/requests"
)

// QuoteRequest asks for an exact-input conversion between the scoped wallets.
type QuoteRequest struct {
	requests.Open
	FromAsset string `form:"from_asset" json:"from_asset"`
	ToAsset   string `form:"to_asset" json:"to_asset"`
	Amount    string `form:"amount" json:"amount"`
}

// Rules validates public parameters; assets are resolved from the custody catalog.
func (*QuoteRequest) Rules(http.Context) map[string]string {
	return map[string]string{"from_asset": "required|max_len:16", "to_asset": "required|max_len:16", "amount": "required|decimal_string|max_len:80"}
}
