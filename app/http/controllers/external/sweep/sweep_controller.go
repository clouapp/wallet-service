package sweep

import (
	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/services/features"
	sweepsvc "github.com/macrowallets/waas/app/services/sweep"
)

// SweepController serves the external consolidate and gas routes (Consolidate,
// GasStatus, GasCheck, Preview). The implementation is shared with the other
// HTTP surface.
type SweepController = controllers.SweepHandler

// NewSweepController wires the external consolidate and gas handlers.
func NewSweepController(sweeps sweepsvc.Service, flags *features.Service) *SweepController {
	return controllers.NewSweepHandler("external", sweeps, flags)
}
