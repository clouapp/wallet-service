package accounts

import (
	"errors"
	"strings"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/policies"
	"github.com/macrowallets/waas/app/services/withdraw"
)

type CreateAccountTokenRequest struct {
	Name             string         `form:"name"              json:"name"`
	ValidUntil       string         `form:"valid_until"       json:"valid_until,omitempty"`
	RequireSignature bool           `form:"require_signature" json:"require_signature,omitempty"`
	Permissions      []string       `form:"permissions"       json:"permissions"`
	IpCidr           string         `form:"ip_cidr"           json:"ip_cidr,omitempty"`
	SpendingLimit    map[string]any `form:"spending_limit"    json:"spending_limit,omitempty"`
}

func (r *CreateAccountTokenRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *CreateAccountTokenRequest) Filters(ctx http.Context) map[string]string {
	return map[string]string{
		"name":    "trim",
		"ip_cidr": "trim",
	}
}

func (r *CreateAccountTokenRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"name":          "required",
		"valid_until":   "rfc3339",
		"permissions":   "array",
		"permissions.*": "in:" + strings.Join(policies.APITokenPermissionCatalog(), ","),
	}
}

// After checks the restrictions no rule reaches, once the rules passed: the
// ip_cidr allowlist, then the spending_limit object. The first one that fails
// is the only field answered.
func (r *CreateAccountTokenRequest) After(ctx http.Context) map[string][]string {
	if !policies.ValidAPITokenIPCIDR(r.IpCidr) {
		return map[string][]string{"ip_cidr": {"The ip_cidr must be a valid CIDR."}}
	}
	if _, err := withdraw.StoreSpendingLimit(r.SpendingLimit); err != nil {
		if errors.Is(err, withdraw.ErrNegativeSpendingLimit) {
			return map[string][]string{"spending_limit.daily_usd": {"must be a decimal string greater than or equal to 0"}}
		}
		return map[string][]string{"spending_limit": {"must be an object with an optional daily_usd decimal"}}
	}
	return nil
}
