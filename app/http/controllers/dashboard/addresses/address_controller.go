package addresses

import (
	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/services/walletops"
)

// AddressController serves the dashboard address routes (Store, Update, Index).
// The implementation is shared with the external surface.
type AddressController = controllers.AddressesHandler

// NewAddressController wires the dashboard address handlers with the wallet operations.
func NewAddressController(ops *walletops.Service) *AddressController {
	return controllers.NewAddressesHandler("dashboard", ops)
}
