package identity

import "github.com/macrowallets/waas/packages/rampengine/app/featureflags"

func Definitions() []featureflags.Definition {
	return []featureflags.Definition{emailVerificationBypass}
}
