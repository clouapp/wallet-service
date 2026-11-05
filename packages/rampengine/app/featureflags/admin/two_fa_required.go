package admin

import "github.com/macrowallets/waas/packages/rampengine/app/featureflags"

// TwoFARequired decides whether an admin login demands a second factor at all.
const TwoFARequired = "admin-2fa-required"

var twoFARequired = featureflags.Definition{
	Name:        TwoFARequired,
	Label:       "Require two-factor authentication",
	Description: "When off, this admin skips the email OTP step and receives a JWT straight from the login endpoint.",
	Scopes:      []featureflags.ScopeType{featureflags.ScopeAdmin},
	Default:     true,
}
