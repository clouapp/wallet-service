package models

import "github.com/goravel/framework/database/orm"

// CustomerBankBeneficiary is a US bank account a customer saved for bank
// payouts (spec §5.4). Every text column but the clear ones holds
// facades.Crypt() ciphertext; repositories.CustomerBankBeneficiaryRepository
// is the only code that reads or writes it.
type CustomerBankBeneficiary struct {
	orm.Model
	orm.SoftDeletes
	ExternalID       string  `gorm:"size:36;not null" json:"-"`
	CustomerID       uint    `gorm:"not null" json:"-"`
	Label            string  `gorm:"size:64;not null" json:"-"`
	AccountClass     string  `gorm:"size:16;not null" json:"-"`
	AccountType      string  `gorm:"size:16;not null" json:"-"`
	Relationship     string  `gorm:"size:32;not null" json:"-"`
	AccountLast4     string  `gorm:"size:4;not null" json:"-"`
	RoutingLast4     string  `gorm:"size:4;not null" json:"-"`
	Fingerprint      string  `gorm:"size:64;not null" json:"-"`
	Revision         int     `gorm:"not null;default:1" json:"-"`
	RoutingNumber    string  `gorm:"type:text;not null" json:"-"`
	AccountNumber    string  `gorm:"type:text;not null" json:"-"`
	BeneficiaryName  string  `gorm:"type:text;not null" json:"-"`
	AddressLine1     string  `gorm:"column:address_line_1;type:text;not null" json:"-"`
	AddressLine2     *string `gorm:"column:address_line_2;type:text" json:"-"`
	City             string  `gorm:"type:text;not null" json:"-"`
	StateCode        string  `gorm:"type:text;not null" json:"-"`
	PostalCode       string  `gorm:"type:text;not null" json:"-"`
	BusinessIndustry *string `gorm:"type:text" json:"-"`
}
