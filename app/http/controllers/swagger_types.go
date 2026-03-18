package controllers

import "github.com/macromarkets/vault/app/models"

// Shared response envelope types used only for Swagger doc generation.

type ChainInfo struct {
	ID                   string `json:"id" example:"eth"`
	Name                 string `json:"name" example:"Ethereum"`
	NativeAsset          string `json:"native_asset" example:"eth"`
	RequiredConfirmations uint64 `json:"required_confirmations" example:"12"`
}

type ErrorResponse struct {
	Error string `json:"error" example:"something went wrong"`
}

type HealthResponse struct {
	Status  string `json:"status" example:"ok"`
	Version string `json:"version" example:"0.1.0"`
}

type ChainListResponse struct {
	Data []ChainInfo `json:"data"`
}

type WalletListResponse struct {
	Data []models.Wallet `json:"data"`
}

type AddressListResponse struct {
	Data []models.Address `json:"data"`
}

type TransactionListResponse struct {
	Data []models.Transaction `json:"data"`
}

type WebhookConfigListResponse struct {
	Data []models.WebhookConfig `json:"data"`
}
