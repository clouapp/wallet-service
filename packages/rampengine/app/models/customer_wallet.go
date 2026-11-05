package models

import "github.com/goravel/framework/database/orm"

// CustomerWallet is an address a customer registered as its own (spec §3.1,
// §9): a business's deposits arrive only to, and its withdrawals leave only
// from, one of these. An address belongs to one holder per chain.
//
// No SoftDeletes: a registration is withdrawn only by compliance, releasing
// one taken by mistake (CustomerWalletRepository.DeleteAudited), and then it
// is gone, the address free for its rightful holder.
type CustomerWallet struct {
	orm.Model
	CustomerID uint `gorm:"not null;index" json:"customer_id"`
	// Chain is the payment rail of the address (ChainSolana).
	Chain string `gorm:"size:16;not null" json:"chain"`
	// Address is the address as the chain spells it: base58 tells upper
	// from lower case, so it is compared exactly.
	Address string `gorm:"size:64;not null" json:"address"`
	// RegisteredByUserID is the member who registered it.
	RegisteredByUserID uint `gorm:"not null" json:"registered_by_user_id"`
}
