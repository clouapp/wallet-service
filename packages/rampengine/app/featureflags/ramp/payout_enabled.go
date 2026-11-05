package ramp

import "github.com/macrowallets/waas/packages/rampengine/app/featureflags"

// PayoutEnabled vetoes the pix payout rail.
const PayoutEnabled = "ramp-payout-enabled"

var payoutEnabled = featureflags.Definition{
	Name:  PayoutEnabled,
	Label: "Off-ramp por transfer",
	Description: "Veto sobre o trilho de saída, independente do KYC. É o jeito de suspender um " +
		"usuário sob investigação sem desativar a conta de depósito nem mexer nos endorsements. " +
		"As ordens já criadas seguem o fluxo até o fim.",
	Scopes:  []featureflags.ScopeType{featureflags.ScopeGlobal, featureflags.ScopeUser},
	Default: true,
}
