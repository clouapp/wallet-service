package swaps

import (
	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/services/swap"
)

// SwapController serves account-token quotes and order reads.
type SwapController = controllers.SwapHandler

// NewSwapController wraps the shared, authenticated swap handlers.
func NewSwapController(service *swap.Service) *SwapController {
	return controllers.NewSwapHandler(service)
}
