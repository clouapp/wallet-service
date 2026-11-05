// Package customers declares the switches over who may hold money at XIP
// besides the person who signs in: today, a company (the KYB).
// app/http/middleware and app/services/client/customers read them.
package customers

import "github.com/macrowallets/waas/packages/rampengine/app/featureflags"

// Definitions is this domain's part of the catalog.
func Definitions() []featureflags.Definition {
	return []featureflags.Definition{kyb}
}
