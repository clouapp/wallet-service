package requests

import (
	"github.com/goravel/framework/contracts/http"
)

type CreateAccountTokenRequest struct {
	Name             string                `form:"name"              json:"name"`
	ValidUntil       string                `form:"valid_until"       json:"valid_until,omitempty"`
	RequireSignature bool                  `form:"require_signature" json:"require_signature,omitempty"`
	Permissions      []string              `form:"permissions"       json:"permissions"`
	IpCidr           string                `form:"ip_cidr"           json:"ip_cidr,omitempty"`
	SpendingLimit    *TokenSpendingLimitIn `form:"spending_limit"    json:"spending_limit,omitempty"`
}

type TokenSpendingLimitIn struct {
	DailyUSD *float64 `form:"daily_usd" json:"daily_usd"`
}

func (r *CreateAccountTokenRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *CreateAccountTokenRequest) Filters(ctx http.Context) map[string]string {
	return map[string]string{
		"name": "trim",
	}
}

func (r *CreateAccountTokenRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"name":        "required",
		"valid_until": "rfc3339",
	}
}
