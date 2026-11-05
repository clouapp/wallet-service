package facades

import (
	"github.com/macrowallets/waas/packages/rampengine/app/container"
	"github.com/macrowallets/waas/packages/rampengine/packages/featureflag"
	"github.com/macrowallets/waas/packages/rampengine/packages/featureflag/contracts"
)

func Feature() contracts.Feature {
	return container.MustMake[contracts.Feature](featureflag.Binding)
}
