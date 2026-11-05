package models

import "github.com/goravel/framework/database/orm"

// Bank account types, as Bridge's checking_or_savings takes them.
const (
	BankAccountTypeChecking = "checking"
	BankAccountTypeSavings  = "savings"
)

// BankAccountAddress is the account holder's address, the one Bridge requires
// on every US external account. It is declared here, not in the ramp package,
// because repositories store it and must never import ramp; ramp aliases it.
type BankAccountAddress struct {
	StreetLine1 string `json:"street_line_1"`
	StreetLine2 string `json:"street_line_2,omitempty"`
	City        string `json:"city"`
	State       string `json:"state"`
	PostalCode  string `json:"postal_code"`
	Country     string `json:"country"`
}

// UserBankAccount is a US bank account a user saved to receive USD payouts.
// Every text column but the last four digits, the type and the hash is
// encrypted at rest by UserBankAccountRepository; this struct holds the
// ciphertext and never leaves the repository.
type UserBankAccount struct {
	orm.Model
	orm.SoftDeletes
	UserID           uint   `gorm:"not null;index" json:"user_id"`
	Label            string `gorm:"type:text;not null" json:"-"`
	AccountOwnerName string `gorm:"type:text;not null" json:"-"`
	BankName         string `gorm:"type:text;not null" json:"-"`
	RoutingNumber    string `gorm:"type:text;not null" json:"-"`
	AccountNumber    string `gorm:"type:text;not null" json:"-"`
	RoutingLast4     string `gorm:"size:4;not null" json:"routing_last4"`
	AccountLast4     string `gorm:"size:4;not null" json:"account_last4"`
	AccountType      string `gorm:"size:16;not null" json:"account_type"`
	// Address is the encrypted JSON of a BankAccountAddress.
	Address     string `gorm:"type:text;not null" json:"-"`
	AccountHash string `gorm:"size:64;not null" json:"-"`
}
