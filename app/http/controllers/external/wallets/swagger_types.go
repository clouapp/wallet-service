package wallets

// CreateWalletSwagger is the request body for creating a wallet (doc-only).
type CreateWalletSwagger struct {
	Chain      string `json:"chain" example:"eth"`
	Label      string `json:"label" example:"My Ethereum Wallet"`
	Passphrase string `json:"passphrase" example:"my-secret-passphrase-12chars"`
}
