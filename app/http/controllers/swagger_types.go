package controllers

import (
	addressresource "github.com/macrowallets/waas/app/http/resources/addresses"
	chainresource "github.com/macrowallets/waas/app/http/resources/chains"
	walletsresources "github.com/macrowallets/waas/app/http/resources/wallets"
	webhookresource "github.com/macrowallets/waas/app/http/resources/webhooks"
)

// Shared response envelope types used only for Swagger doc generation.

// ChainInfo documents one chain of the chains list. Finality is the rule that
// confirms the chain's transactions: confirmations (RequiredConfirmations of them),
// finalized_tag (EVM, the block is at or below the node's finalized block) or
// solidified (TRON, at or below the solidified block); under the last two
// RequiredConfirmations is only the typical finality lag, for display.
type ChainInfo struct {
	ID                    string `json:"id" example:"eth"`
	Name                  string `json:"name" example:"Ethereum"`
	NativeAsset           string `json:"native_asset" example:"eth"`
	RequiredConfirmations uint64 `json:"required_confirmations" example:"12"`
	Finality              string `json:"finality" example:"confirmations" enums:"confirmations,finalized_tag,solidified"`
}

type ChainListResponse struct {
	Data []ChainInfo `json:"data"`
}

// ChainTokenListResponse documents the tokens of one chain.
type ChainTokenListResponse struct {
	Data []chainresource.Token `json:"data"`
}

type WalletListResponse struct {
	Data []walletsresources.ListItem `json:"data"`
}

type AddressListResponse struct {
	Data []addressresource.Address `json:"data"`
}

type WebhookConfigListResponse struct {
	Data []webhookresource.WebhookConfig `json:"data"`
}
