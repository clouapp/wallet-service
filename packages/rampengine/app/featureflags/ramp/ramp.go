// Package ramp declares the switches of the Pix, USD and bank payout rails. ramp.Rail.Flag in
// app/adapters/ramp names which one closes each rail.
package ramp

import "github.com/macrowallets/waas/packages/rampengine/app/featureflags"

// Definitions is this domain's part of the catalog, sorted by name like the
// catalog itself.
func Definitions() []featureflags.Definition {
	return []featureflags.Definition{bankPayoutEnabled, payinEnabled, payoutEnabled, usdPayinEnabled, usdPayoutEnabled}
}
