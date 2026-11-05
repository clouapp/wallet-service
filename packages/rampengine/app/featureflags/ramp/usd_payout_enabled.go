package ramp

import "github.com/macrowallets/waas/packages/rampengine/app/featureflags"

// USDPayoutEnabled vetoes both USD payout rails, ACH and wire.
const USDPayoutEnabled = "ramp-usd-payout-enabled"

var usdPayoutEnabled = featureflags.Definition{
	Name:  USDPayoutEnabled,
	Label: "Saque USD por liquidation address",
	Description: "Quando desligada, novos endereços de saque USD são recusados e um drain novo " +
		"não abre transação. As transações já abertas seguem até o fim.",
	Scopes:  []featureflags.ScopeType{featureflags.ScopeGlobal, featureflags.ScopeUser},
	Default: true,
}
