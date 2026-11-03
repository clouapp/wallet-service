package ramps

import (
	"fmt"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

const (
	DepositModeSimulated = "simulated"
	DepositModeOnChain   = "on_chain"
	// DepositModePrepared is a payout funded by signing what the provider
	// prepared, not by a deposit to an address.
	DepositModePrepared = "prepared"
)

// DepositModeForProvider maps a provider's settings mode to the client contract.
func DepositModeForProvider(config *ramp.Config, provider string) (string, error) {
	state, err := config.Provider(ramp.ProviderCode(provider))
	if err != nil {
		return "", fmt.Errorf("ramps: load provider settings: %w", err)
	}
	switch state.Mode {
	case models.ProviderModeSandbox:
		return DepositModeSimulated, nil
	case models.ProviderModeProduction:
		return DepositModeOnChain, nil
	default:
		return "", fmt.Errorf("provider %q: unknown mode %q", provider, state.Mode)
	}
}
