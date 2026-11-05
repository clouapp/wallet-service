package ramp

import "github.com/macrowallets/waas/packages/rampengine/app/featureflags"

// BankPayoutEnabled opens the bank payout rails, ach_payout and wire_payout.
//
// Unlike the pix switches it defaults to off. Its global value is a veto over
// every user (catalog.Register gates each global-scoped flag by it), and a user
// with no override reads the default — so the beta is the global switched on
// plus a per-user override.
//
// The launch is not this Default alone. The flag store memoizes the default on
// first read (packages/featureflag getByKey → SetIfAbsent), so every read
// before launch stored a false that looks exactly like a veto. Launching flips
// Default to true and purges this flag's stored false rows in a migration,
// then re-applies the vetoes that were deliberate.
const BankPayoutEnabled = "ramp-bank-payout-enabled"

var bankPayoutEnabled = featureflags.Definition{
	Name:  BankPayoutEnabled,
	Label: "Saque para conta bancária (EUA)",
	Description: "Abre os trilhos de saque por ACH e wire. Desligado por padrão: para liberar um " +
		"usuário, ligue o global e ative para ele. Desligar o global fecha o trilho para todos; " +
		"as ordens já criadas seguem o fluxo até o fim.",
	Scopes:  []featureflags.ScopeType{featureflags.ScopeGlobal, featureflags.ScopeUser},
	Default: false,
}
