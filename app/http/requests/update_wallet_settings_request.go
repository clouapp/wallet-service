package requests

import (
	"github.com/goravel/framework/contracts/http"
)

type UpdateWalletSettingsRequest struct {
	FeeRateMin        string `form:"fee_rate_min"        json:"fee_rate_min,omitempty"`
	FeeRateMax        string `form:"fee_rate_max"        json:"fee_rate_max,omitempty"`
	FeeMultiplier     string `form:"fee_multiplier"      json:"fee_multiplier,omitempty"`
	RequiredApprovals string `form:"required_approvals"  json:"required_approvals,omitempty"`
	FrozenUntil       string `form:"frozen_until"        json:"frozen_until,omitempty"`
}

func (r *UpdateWalletSettingsRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *UpdateWalletSettingsRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"fee_rate_min":       "decimal_string",
		"fee_rate_max":       "decimal_string",
		"fee_multiplier":     "decimal_string",
		"required_approvals": "integer_string",
		"frozen_until":       "rfc3339",
	}
}
