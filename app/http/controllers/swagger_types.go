package controllers

// Shared response envelope types used only for Swagger doc generation.

type ChainInfo struct {
	ID                    string `json:"id" example:"eth"`
	Name                  string `json:"name" example:"Ethereum"`
	NativeAsset           string `json:"native_asset" example:"eth"`
	RequiredConfirmations uint64 `json:"required_confirmations" example:"12"`
}

type ErrorResponse struct {
	Error string `json:"error" example:"something went wrong"`
}

type HealthResponse struct {
	Status         string               `json:"status" example:"ok"`
	Version        string               `json:"version" example:"0.1.0"`
	DepositScanner DepositScannerHealth `json:"deposit_scanner"`
}

// DepositScannerHealth reports the blocks whose deposits failed to record and wait for
// the reprocessor, per chain. Status is ok, pending_blocks or pending_store_unavailable.
type DepositScannerHealth struct {
	Status       string         `json:"status" example:"ok"`
	Pending      map[string]int `json:"pending"`
	PendingTotal int            `json:"pending_total" example:"0"`
	Error        string         `json:"error,omitempty"`
}

type ChainListResponse struct {
	Data []ChainInfo `json:"data"`
}

type WalletListResponse struct {
	Data []WalletListItem `json:"data"`
}

type AddressListResponse struct {
	Data []AddressView `json:"data"`
}

type WalletTransactionListResponse struct {
	Data []WalletTransactionView `json:"data"`
}

type WebhookConfigListResponse struct {
	Data []WebhookConfigView `json:"data"`
}
