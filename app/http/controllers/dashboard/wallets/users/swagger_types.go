package users

import walletusers "github.com/macrowallets/waas/app/http/resources/dashboard/wallets/users"

// Swagger request and response types of the wallet user routes (doc-only).

type AddWalletUserSwagger struct {
	UserID string `json:"user_id" example:"00000000-0000-0000-0000-000000000001"`
	Roles  string `json:"roles" example:"viewer"`
}

type WalletUserListResponse struct {
	Data []walletusers.WalletUser `json:"data"`
}
