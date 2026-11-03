package ramps

// RegisterWalletDTO is POST /api/wallets: an address the customer in scope
// registers as its own, and the step-up code that authorizes it.
type RegisterWalletDTO struct {
	Chain    string
	Address  string
	Code     string
	CodeType string
} //	@name	ClientRegisterWalletDTO
