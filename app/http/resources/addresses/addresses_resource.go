package addresses

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/models"
)

// Address is the deposit address HTTP clients read. Field order and tags
// match the model wire, including embedded timestamps. EncryptedPrivateKey,
// EncryptionIV and EncryptionSalt stay off the wire. A nil page stays nil; an
// empty page stays empty. A nil address stays null. A nil related wallet stays
// omitted. Callers pass the wallet body view, so share material stays off the wire.
type Address struct {
	CreatedAt       *carbon.DateTime `json:"created_at"`
	UpdatedAt       *carbon.DateTime `json:"updated_at"`
	ID              uuid.UUID        `json:"id"`
	WalletID        uuid.UUID        `json:"wallet_id"`
	Chain           string           `json:"chain"`
	Address         string           `json:"address"`
	DerivationIndex int              `json:"derivation_index"`
	ExternalUserID  string           `json:"external_user_id"`
	Metadata        string           `json:"metadata"`
	IsActive        bool             `json:"is_active"`
	Label           string           `json:"label,omitempty"`
	CreatedBy       *uuid.UUID       `json:"created_by,omitempty"`
	DerivationType  string           `json:"derivation_type"`
	Wallet          any              `json:"wallet,omitempty"`
}

// AddressFrom projects one address. A nil wallet stays omitted.
func AddressFrom[W any](addr models.Address, wallet *W) Address {
	var related any
	if wallet != nil {
		related = wallet
	}
	return Address{
		CreatedAt:       addr.CreatedAt,
		UpdatedAt:       addr.UpdatedAt,
		ID:              addr.ID,
		WalletID:        addr.WalletID,
		Chain:           addr.Chain,
		Address:         addr.Address,
		DerivationIndex: addr.DerivationIndex,
		ExternalUserID:  addr.ExternalUserID,
		Metadata:        addr.Metadata,
		IsActive:        addr.IsActive,
		Label:           addr.Label,
		CreatedBy:       addr.CreatedBy,
		DerivationType:  addr.DerivationType,
		Wallet:          related,
	}
}

// AddressesFrom copies a page. A nil slice stays nil; an empty slice stays empty.
func AddressesFrom[W any](addrs []models.Address, walletOf func(*models.Wallet) *W) []Address {
	if addrs == nil {
		return nil
	}
	views := make([]Address, len(addrs))
	for i := range addrs {
		views[i] = AddressFrom(addrs[i], walletOf(addrs[i].Wallet))
	}
	return views
}

// AddressPtr keeps a nil address as JSON null.
func AddressPtr[W any](addr *models.Address, walletOf func(*models.Wallet) *W) *Address {
	if addr == nil {
		return nil
	}
	view := AddressFrom(*addr, walletOf(addr.Wallet))
	return &view
}
