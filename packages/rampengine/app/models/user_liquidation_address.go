package models

import (
	"time"

	"github.com/goravel/framework/database/orm"
)

// UserLiquidationAddress is a customer's standing payout address at one
// provider: a crypto address that drains every deposit to one of the user's US
// bank accounts over one rail. Like CustomerVirtualAccount it inverts the order
// lifecycle: the address exists first, and each drain into it creates its own
// transaction.
type UserLiquidationAddress struct {
	orm.Model
	orm.SoftDeletes
	// CustomerID is whose payout it is; the provider holds the address on that
	// customer. UserID is the member who provisioned it and owns the bank
	// account.
	CustomerID uint   `gorm:"not null;index" json:"customer_id"`
	UserID     uint   `gorm:"not null;index" json:"user_id"`
	Provider   string `gorm:"size:32;not null" json:"provider"`
	// UserBankAccountID is the bank account the drains pay. No foreign key: the
	// account is soft-deleted and its addresses stay as history.
	UserBankAccountID uint `gorm:"not null" json:"user_bank_account_id"`
	// Rail is usd_ach_payout or usd_wire_payout.
	Rail  string `gorm:"size:24;not null" json:"rail"`
	Chain string `gorm:"size:32;not null" json:"chain"`
	// Currency is the token the user sends, lowercase.
	Currency string `gorm:"size:16;not null" json:"currency"`
	// ExternalAddressID is the provider's id for the address, or the
	// reservation's placeholder while it is provisioning.
	ExternalAddressID string `gorm:"size:64;not null" json:"external_address_id"`
	// Address is where the user sends the crypto: base58, case-sensitive.
	Address string `gorm:"size:128;not null" json:"address"`
	// ReturnAddress is the user's wallet, where a failed drain is sent back.
	ReturnAddress string `gorm:"size:64;not null" json:"return_address"`
	// Reference is the ACH reference or the wire message, nil for none.
	Reference *string `gorm:"size:140" json:"reference,omitempty"`
	// FeePercent is XIP's cut, fixed on the address when it was provisioned.
	FeePercent *string `gorm:"type:numeric(5,2)" json:"fee_percent,omitempty"`
	Status     string  `gorm:"size:16;not null" json:"status"`
	// RawPayload is a string for the reason Transaction documents (text OID,
	// cast to jsonb by Postgres). It names the bank account and is never logged.
	RawPayload   string     `gorm:"type:jsonb;not null" json:"-"`
	LastSyncedAt *time.Time `json:"last_synced_at,omitempty"`
}

// IsActive reports whether the address still pays out.
func (a *UserLiquidationAddress) IsActive() bool { return a.Status == LiquidationAddressStatusActive }
