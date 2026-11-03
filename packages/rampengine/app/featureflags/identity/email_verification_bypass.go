package identity

import "github.com/macrowallets/waas/packages/rampengine/app/featureflags"

// EmailVerificationBypass lets POST /api/login issue a session after password
// only — no login email OTP — even when email_verified_at is empty.
const EmailVerificationBypass = "email-verification-bypass"

var emailVerificationBypass = featureflags.Definition{
	Name:  EmailVerificationBypass,
	Label: "Login só com senha (sem OTP)",
	Description: "Quando ligada, POST /api/login devolve o JWT após senha correta, " +
		"sem enviar código por e-mail. Não marca o e-mail como verificado no cadastro.",
	Scopes:  []featureflags.ScopeType{featureflags.ScopeUser},
	Default: false,
}
