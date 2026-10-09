package controllers

import (
	addressresource "github.com/macrowallets/waas/app/http/resources/addresses"
	walletsresources "github.com/macrowallets/waas/app/http/resources/wallets"
	webhookresource "github.com/macrowallets/waas/app/http/resources/webhooks"
)

// Shared response envelope types used only for Swagger doc generation.

type ChainInfo struct {
	ID                    string `json:"id" example:"eth"`
	Name                  string `json:"name" example:"Ethereum"`
	NativeAsset           string `json:"native_asset" example:"eth"`
	RequiredConfirmations uint64 `json:"required_confirmations" example:"12"`
}

type ChainListResponse struct {
	Data []ChainInfo `json:"data"`
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
