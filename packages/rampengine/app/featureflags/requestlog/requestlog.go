// Package requestlog declares the switch of the request logger middleware.
package requestlog

import "github.com/macrowallets/waas/packages/rampengine/app/featureflags"

// Definitions is this domain's part of the catalog.
func Definitions() []featureflags.Definition {
	return []featureflags.Definition{enabled}
}
