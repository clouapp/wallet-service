package requests

import (
	"strings"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/policies"
)

type CreateAccountTokenRequest struct {
	Name             string   `form:"name"              json:"name"`
	ValidUntil       string   `form:"valid_until"       json:"valid_until,omitempty"`
	RequireSignature bool     `form:"require_signature" json:"require_signature,omitempty"`
	Permissions      []string `form:"permissions"       json:"permissions"`
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
		"name":          "required",
		"valid_until":   "rfc3339",
		"permissions":   "array",
		"permissions.*": "in:" + strings.Join(policies.APITokenPermissionCatalog(), ","),
	}
}
