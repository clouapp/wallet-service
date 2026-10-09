package settings

import "time"

// Swagger request types of the settings routes (doc-only).

// UpdateWalletSettingsSwagger lists the accepted fields; omit a field to keep
// it, send null to reset it.
type UpdateWalletSettingsSwagger struct {
	Label             *string  `json:"label,omitempty" example:"Treasury"`
	FeeRateMin        *int     `json:"fee_rate_min,omitempty" example:"2"`
	FeeRateMax        *int     `json:"fee_rate_max,omitempty" example:"50"`
	FeeMultiplier     *float64 `json:"fee_multiplier,omitempty" example:"1.25"`
	RequiredApprovals *int     `json:"required_approvals,omitempty" example:"1"`
}

type FreezeWalletSwagger struct {
	FrozenUntil *time.Time `json:"frozen_until,omitempty"`
}
