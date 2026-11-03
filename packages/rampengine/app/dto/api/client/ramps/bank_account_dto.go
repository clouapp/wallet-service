package ramps

import "github.com/macrowallets/waas/packages/rampengine/app/models"

// CreateBankAccountDTO saves a US bank account to receive USD payouts. Code and
// CodeType carry the MFA step-up: this is where a new payout destination
// enters, so a stolen session must not be enough.
type CreateBankAccountDTO struct {
	Label         string
	OwnerName     string
	BankName      string
	RoutingNumber string
	AccountNumber string
	// AccountType is "checking" or "savings".
	AccountType string
	Address     models.BankAccountAddress
	Code        string
	CodeType    string
} //	@name	ClientCreateBankAccountDTO
