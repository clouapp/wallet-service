package admin

import "github.com/macrowallets/waas/packages/rampengine/app/featureflags"

// TOTP decides whether the authenticator is offered to an admin, not whether
// they use one — that is a confirmed enrollment.
const TOTP = "admin-totp"

var totp = featureflags.Definition{
	Name:  TOTP,
	Label: "Authenticator app (TOTP)",
	Description: "When on, this admin can enroll an authenticator app and is challenged by it " +
		"instead of the emailed code. When off, the feature is not offered: the enrollment " +
		"endpoints refuse and login falls back to the emailed code even if an enrollment exists.",
	Scopes:  []featureflags.ScopeType{featureflags.ScopeAdmin},
	Default: false,
}
