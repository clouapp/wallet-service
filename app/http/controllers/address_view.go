package controllers

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/models"
)

// AddressView is the deposit address HTTP clients read. Field order and tags
// match the model wire, including embedded timestamps. EncryptedPrivateKey,
// EncryptionIV and EncryptionSalt stay off the wire. A nil page stays nil; an
// empty page stays empty. A nil address stays null. A nil related wallet stays
// omitted, and a nested wallet is the wallet body view.
type AddressView struct {
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
	Wallet          *WalletBodyView  `json:"wallet,omitempty"`
}

func newAddressView(addr models.Address) AddressView {
	return AddressView{
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
		Wallet:          walletBodyViewPtr(addr.Wallet),
	}
}

func addressViewPtr(addr *models.Address) *AddressView {
	if addr == nil {
		return nil
	}
	view := newAddressView(*addr)
	return &view
}

// AddressViews copies a page. A nil slice stays nil; an empty slice stays empty.
func AddressViews(addrs []models.Address) []AddressView {
	if addrs == nil {
		return nil
	}
	views := make([]AddressView, len(addrs))
	for i := range addrs {
		views[i] = newAddressView(addrs[i])
	}
	return views
}

// AddressViewPtr keeps a nil address as JSON null.
func AddressViewPtr(addr *models.Address) *AddressView {
	return addressViewPtr(addr)
}
