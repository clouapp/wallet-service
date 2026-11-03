package requestlog

import "github.com/macrowallets/waas/packages/rampengine/app/featureflags"

// Enabled switches the request logger middleware.
const Enabled = "request-logging"

var enabled = featureflags.Definition{
	Name:        Enabled,
	Label:       "HTTP request logging",
	Description: "When off, the request logger middleware becomes a no-op and nothing is enqueued for the admin explorer.",
	Scopes:      nil,
	Default:     true,
}
