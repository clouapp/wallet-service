package customers

import "github.com/macrowallets/waas/packages/rampengine/app/featureflags"

// Kyb opens the company wallet to a user: POST /api/customers creates a
// business draft only while it is on for them, and GET /api/customers tells
// the app so (can_open_business), the client API exposing no flag itself.
//
// Per user only, never product-wide (KYB spec §13): a flag declaring the
// global scope is gated by it, and a global "off" would veto the pilot user it
// was turned on for. Off by default everywhere; an operator turns it on per
// user in the admin (on staging for whoever tests it).
const Kyb = "kyb"

var kyb = featureflags.Definition{
	Name:  Kyb,
	Label: "Carteira de empresa (KYB)",
	Description: "Quando ligada para o usuário, o app oferece a carteira de empresa (PJ) e o cadastro da empresa " +
		"(POST /api/customers) é aceito. Desligada, a opção some do app e o cadastro de empresa responde 404. " +
		"Quem já tem empresa cadastrada continua com ela.",
	Scopes:  []featureflags.ScopeType{featureflags.ScopeUser},
	Default: false,
}
