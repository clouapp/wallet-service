package chains

import (
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/models"
)

// Chain is the chain row HTTP clients read. Field order and tags match the
// model wire, including embedded timestamps. The RPC URL and threshold fields
// stay off the wire. A nil page stays nil; an empty page stays empty. A nil
// chain stays null. A non-nil empty icon URL stays "".
type Chain struct {
	CreatedAt             *carbon.DateTime `json:"created_at"`
	UpdatedAt             *carbon.DateTime `json:"updated_at"`
	ID                    string           `json:"id"`
	Name                  string           `json:"name"`
	AdapterType           string           `json:"adapter_type"`
	NativeSymbol          string           `json:"native_symbol"`
	NativeDecimals        int              `json:"native_decimals"`
	NetworkID             *int64           `json:"network_id,omitempty"`
	IsTestnet             bool             `json:"is_testnet"`
	MainnetChainID        *string          `json:"mainnet_chain_id,omitempty"`
	RequiredConfirmations int              `json:"required_confirmations"`
	IconURL               *string          `json:"icon_url,omitempty"`
	DisplayOrder          int              `json:"display_order"`
	Status                string           `json:"status"`
}

// ChainFrom projects one chain.
func ChainFrom(chain models.Chain) Chain {
	return Chain{
		CreatedAt:             chain.CreatedAt,
		UpdatedAt:             chain.UpdatedAt,
		ID:                    chain.ID,
		Name:                  chain.Name,
		AdapterType:           chain.AdapterType,
		NativeSymbol:          chain.NativeSymbol,
		NativeDecimals:        chain.NativeDecimals,
		NetworkID:             chain.NetworkID,
		IsTestnet:             chain.IsTestnet,
		MainnetChainID:        chain.MainnetChainID,
		RequiredConfirmations: chain.RequiredConfirmations,
		IconURL:               chain.IconURL,
		DisplayOrder:          chain.DisplayOrder,
		Status:                chain.Status,
	}
}

// ChainsFrom copies a page. A nil slice stays nil; an empty slice stays empty.
func ChainsFrom(chains []models.Chain) []Chain {
	if chains == nil {
		return nil
	}
	views := make([]Chain, len(chains))
	for i := range chains {
		views[i] = ChainFrom(chains[i])
	}
	return views
}

// ChainPtr keeps a nil chain as JSON null.
func ChainPtr(chain *models.Chain) *Chain {
	if chain == nil {
		return nil
	}
	view := ChainFrom(*chain)
	return &view
}
