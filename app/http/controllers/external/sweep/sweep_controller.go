package sweep

import "github.com/macrowallets/waas/app/http/controllers"

// SweepController serves the external consolidate and gas routes. The
// implementation is shared with the other HTTP surface.
type SweepController = controllers.SweepHandler

// SweepControllerDeps is everything the external sweep controller needs.
// Sweeps and Flags are required.
type SweepControllerDeps = controllers.SweepHandlerDeps

// NewSweepController wires the external consolidate and gas handlers from SweepControllerDeps.
func NewSweepController(deps SweepControllerDeps) *SweepController {
	return controllers.NewSweepHandler("external", deps)
}
