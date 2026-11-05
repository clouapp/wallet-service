// Package admin declares the feature flags scoped to back-office accounts.
package admin

import "github.com/macrowallets/waas/packages/rampengine/app/featureflags"

// Definitions is this domain's part of the catalog.
func Definitions() []featureflags.Definition {
	return []featureflags.Definition{twoFARequired, totp}
}
