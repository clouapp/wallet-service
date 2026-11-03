package models

import "github.com/goravel/framework/database/orm"

// BankAccountDestination is a user's bank account as a provider registered it
// for a customer's payouts, and the provider's id for it. The next liquidation
// address for the account names that id: Bridge masks the account number, so
// the id is the only way back to the account. It is the customer's: the
// provider registered it on the customer's own, so a person and their company
// paying the same account hold one each.
type BankAccountDestination struct {
	orm.Model
	CustomerID        uint   `gorm:"not null" json:"customer_id"`
	UserBankAccountID uint   `gorm:"not null" json:"user_bank_account_id"`
	Provider          string `gorm:"size:32;not null" json:"provider"`
	DestinationRef    string `gorm:"size:64;not null" json:"destination_ref"`
	// PaymentRails is the JSON list of rails the provider said the account
	// takes; "[]" when it said nothing, which refuses no rail.
	PaymentRails string `gorm:"type:text;not null;default:'[]'" json:"payment_rails"`
}
