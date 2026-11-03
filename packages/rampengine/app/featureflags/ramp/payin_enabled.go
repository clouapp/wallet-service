package ramp

import "github.com/macrowallets/waas/packages/rampengine/app/featureflags"

// PayinEnabled vetoes the pix payin rail.
const PayinEnabled = "ramp-payin-enabled"

var payinEnabled = featureflags.Definition{
	Name:  PayinEnabled,
	Label: "On-ramp por conta de depósito",
	Description: "Quando desligada, a conta de depósito permanente para de abrir transação a " +
		"cada depósito recebido, e o provisionamento de novas contas é recusado. As ordens já " +
		"criadas seguem o fluxo até o fim.",
	Scopes:  []featureflags.ScopeType{featureflags.ScopeGlobal, featureflags.ScopeUser},
	Default: true,
}
