package settings

import (
	"time"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/numeric"
)

// Settings is a wallet's fee, approval and freeze settings.
type Settings struct {
	Label             string              `json:"label"`
	FeeRateMin        *int                `json:"fee_rate_min"`
	FeeRateMax        *int                `json:"fee_rate_max"`
	FeeMultiplier     numeric.NullDecimal `json:"fee_multiplier"`
	RequiredApprovals int                 `json:"required_approvals"`
	FrozenUntil       *time.Time          `json:"frozen_until"`
	Status            string              `json:"status"`
}

// NewSettings projects a wallet's settings.
func NewSettings(wallet *models.Wallet) Settings {
	return Settings{
		Label:             wallet.Label,
		FeeRateMin:        wallet.FeeRateMin,
		FeeRateMax:        wallet.FeeRateMax,
		FeeMultiplier:     wallet.FeeMultiplier,
		RequiredApprovals: wallet.RequiredApprovals,
		FrozenUntil:       wallet.FrozenUntil,
		Status:            wallet.Status,
	}
}

// Frozen is the answer to a freeze: the status and when the freeze ends. The
// fields are in the order the JSON object has always been written (sorted by key).
type Frozen struct {
	FrozenUntil time.Time `json:"frozen_until"`
	Status      string    `json:"status"`
}

// NewFrozen projects a freeze that ends at until.
func NewFrozen(until time.Time) Frozen {
	return Frozen{FrozenUntil: until, Status: models.WalletStatusFrozen}
}
