package ramp

import "github.com/macrowallets/waas/packages/rampengine/app/featureflags"

// USDPayinEnabled vetoes the USD payin rail.
const USDPayinEnabled = "ramp-usd-payin-enabled"

var usdPayinEnabled = featureflags.Definition{
	Name:  USDPayinEnabled,
	Label: "Depósito USD por conta virtual",
	Description: "Quando desligada, a conta virtual USD para de abrir transação a cada depósito " +
		"recebido, e o provisionamento de novas contas USD é recusado. As transações já abertas " +
		"seguem até o fim.",
	Scopes:  []featureflags.ScopeType{featureflags.ScopeGlobal, featureflags.ScopeUser},
	Default: true,
}
