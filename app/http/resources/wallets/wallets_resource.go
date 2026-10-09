// Package wallets holds the wallet bodies both HTTP surfaces serve: the list
// entry and the answers to a wallet creation.
package wallets

import (
	walletresource "github.com/macrowallets/waas/app/http/resources/dashboard/wallets"
	walletbalances "github.com/macrowallets/waas/app/http/resources/dashboard/wallets/balances"
	"github.com/macrowallets/waas/app/services/wallet"
	"github.com/macrowallets/waas/app/services/walletview"
)

// network is the network a wallet's chain record really points at (for example
// polygon-amoy) and whether it is a test network, so clients can pick the
// matching block explorer and flag testnet wallets whatever the account
// environment. Network is omitted when the chain cannot be resolved.
type network struct {
	Network string `json:"network,omitempty" example:"polygon-amoy"`
	Testnet bool   `json:"testnet" example:"true"`
}

// ListItem is a list entry: the wallet body, its network and the native and
// configured token balances of its last refresh (as GET /wallets/{id}/balances).
// Timestamps stay in the stored carbon format. A wallet without balances has
// "assets": [].
type ListItem struct {
	walletresource.Wallet
	network
	Assets []walletbalances.Balance `json:"assets"`
}

// NewListItem projects one wallet of a page.
func NewListItem(item walletview.Item) ListItem {
	assets := walletbalances.BalancesFrom(item.Assets, walletresource.WalletPtr)
	if assets == nil {
		assets = []walletbalances.Balance{}
	}
	return ListItem{
		Wallet:  walletresource.WalletFrom(item.Wallet),
		network: network{Network: item.Network.Name, Testnet: item.Network.Testnet},
		Assets:  assets,
	}
}

// NewListItems projects a page of wallets.
func NewListItems(items []walletview.Item) []ListItem {
	views := make([]ListItem, 0, len(items))
	for _, item := range items {
		views = append(views, NewListItem(item))
	}
	return views
}

// Creation is the answer to a wallet creation in the dashboard: the wallet, the
// combined public key and the activation code. The customer share, the
// passphrase and the service share are not on it. The fields are in the order
// the JSON object has always been written (sorted by key).
type Creation struct {
	ActivationCode   string                 `json:"activation_code"`
	ServicePublicKey string                 `json:"service_public_key"`
	Wallet           *walletresource.Wallet `json:"wallet"`
}

// NewCreation projects a created wallet.
func NewCreation(result *wallet.CreateWalletResult) Creation {
	return Creation{
		ActivationCode:   result.ActivationCode,
		ServicePublicKey: result.ServicePublicKey,
		Wallet:           walletresource.WalletPtr(result.Wallet),
	}
}

// Created is the external answer to a wallet creation: the wallet fields and the
// combined public key. The customer share, the passphrase, the service share
// and the activation code are not on it.
type Created struct {
	walletresource.Wallet
	// Hex of the combined MPC public key.
	ServicePublicKey string `json:"service_public_key" example:"02a1b2c3..."`
}

// NewCreated projects a created wallet for the external API.
func NewCreated(result *wallet.CreateWalletResult) Created {
	return Created{
		Wallet:           walletresource.WalletFrom(*result.Wallet),
		ServicePublicKey: result.ServicePublicKey,
	}
}
