package controllers

import (
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/models"
)

// ChainView is the chain row HTTP clients read. Field order and tags match the
// model wire, including embedded timestamps. The RPC URL and threshold fields
// stay off the wire. A nil page stays nil; an empty page stays empty. A nil
// chain stays null. A non-nil empty icon URL stays "".
type ChainView struct {
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

func newChainView(chain models.Chain) ChainView {
	return ChainView{
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

// ChainViews copies a page. A nil slice stays nil; an empty slice stays empty.
func ChainViews(chains []models.Chain) []ChainView {
	if chains == nil {
		return nil
	}
	views := make([]ChainView, len(chains))
	for i := range chains {
		views[i] = newChainView(chains[i])
	}
	return views
}

// ChainViewPtr keeps a nil chain as JSON null.
func ChainViewPtr(chain *models.Chain) *ChainView {
	if chain == nil {
		return nil
	}
	view := newChainView(*chain)
	return &view
}
