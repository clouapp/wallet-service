package ramps

import (
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/support/wiretime"
)

// WalletResource is an address a customer registered as its own (API contract
// §9): POST /api/wallets answers it and GET /api/customers lists them.
type WalletResource struct {
	Chain     string    `json:"chain"`
	Address   string    `json:"address"`
	CreatedAt time.Time `json:"created_at"`
} //	@name	ClientWalletResource

// NewWalletResource projects a registration.
func NewWalletResource(wallet *models.CustomerWallet) WalletResource {
	resource := WalletResource{Chain: wallet.Chain, Address: wallet.Address}
	if wallet.CreatedAt != nil {
		resource.CreatedAt = wiretime.Of(wallet.CreatedAt.StdTime())
	}
	return resource
}

// NewWalletCollection projects a customer's registrations, never null.
func NewWalletCollection(wallets []models.CustomerWallet) []WalletResource {
	out := make([]WalletResource, 0, len(wallets))
	for i := range wallets {
		out = append(out, NewWalletResource(&wallets[i]))
	}
	return out
}
