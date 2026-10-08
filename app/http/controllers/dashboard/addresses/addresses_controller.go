package addresses

import "github.com/macrowallets/waas/app/http/controllers"

// AddressesController serves the dashboard address routes. The implementation
// is shared with the external surface.
type AddressesController = controllers.AddressesHandler

// AddressesControllerDeps is everything the dashboard addresses controller needs.
// Every field is required.
type AddressesControllerDeps = controllers.AddressesHandlerDeps

// NewAddressesController wires the dashboard address handlers from AddressesControllerDeps.
func NewAddressesController(deps AddressesControllerDeps) *AddressesController {
	return controllers.NewAddressesHandler("dashboard", deps)
}
