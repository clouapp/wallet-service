package blindpay

import (
	"context"
	"fmt"
	"strings"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// Leg is the network and token of a stablecoin leg, spelled the way BlindPay
// takes them.
type Leg struct {
	Network string
	Token   string
}

// LegFor translates what the product offers into what BlindPay settles.
//
// It is the one place that knows a development instance only accepts USDB, its
// test stablecoin, on devnet: the app and the API keep saying USDC and USDT,
// and the sandbox settles both as USDB. Production settles each as itself on
// mainnet. Anything else is not something BlindPay serves here.
func LegFor(mode, chain, token string) (Leg, error) {
	if !strings.EqualFold(strings.TrimSpace(chain), models.ChainSolana) {
		return Leg{}, fmt.Errorf("blindpay: chain %q: %w", chain, ramp.ErrNoProviderAvailable)
	}
	token = strings.ToUpper(strings.TrimSpace(token))
	if token != models.TargetTokenUSDC && token != models.TargetTokenUSDT {
		return Leg{}, fmt.Errorf("blindpay: token %q: %w", token, ramp.ErrNoProviderAvailable)
	}
	switch mode {
	case models.ProviderModeSandbox:
		return Leg{Network: "solana_devnet", Token: "USDB"}, nil
	case models.ProviderModeProduction:
		return Leg{Network: "solana", Token: token}, nil
	}
	return Leg{}, fmt.Errorf("blindpay: mode %q: %w", mode, ramp.ErrNoProviderAvailable)
}

var _ ramp.PayoutLegNamer = (*Adapter)(nil)

// PayoutLeg is LegFor in the mode of the instance in use: the network and
// token a payout of this instance answers, which the binding is written in.
func (a *Adapter) PayoutLeg(ctx context.Context, chain, token string) (string, string, error) {
	client, err := a.client(ctx)
	if err != nil {
		return "", "", err
	}
	leg, err := LegFor(client.Mode(), chain, token)
	if err != nil {
		return "", "", err
	}
	return leg.Network, leg.Token, nil
}
