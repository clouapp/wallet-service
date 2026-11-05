package repositories

import (
	"errors"
	"fmt"

	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// ErrWalletTaken refuses registering an address another holder registered
// first: the unique (chain, address) settles two that race.
var ErrWalletTaken = errors.New("wallet registered to another customer")

// ErrWalletGone answers the release of a registration released since it was
// read.
var ErrWalletGone = errors.New("wallet registration gone")

// CustomerWalletRepository is the persistence boundary for the addresses a
// customer registered as its own (spec §3.1, §9).
type CustomerWalletRepository interface {
	// FindByAddress is the registration of address on chain, whoever holds
	// it, or nil. The address is compared exactly: base58 tells upper from
	// lower case. A money rule asks whether an address is a customer's own by
	// comparing CustomerID with the customer in scope.
	FindByAddress(chain, address string) (*models.CustomerWallet, error)
	// ListByCustomers is the wallets of each of customerIDs, oldest first.
	ListByCustomers(customerIDs []uint) ([]models.CustomerWallet, error)
	// Create registers wallet. ErrWalletTaken when another registration holds
	// its address.
	Create(wallet *models.CustomerWallet) error
	// FindByCustomerAndID is the registration walletID of customerID, or nil.
	FindByCustomerAndID(customerID, walletID uint) (*models.CustomerWallet, error)
	// DeleteAudited releases wallet — a registration compliance found taken
	// by mistake — and records entry, in one transaction: the address is then
	// free for its rightful holder. ErrWalletGone when it went since read.
	DeleteAudited(wallet *models.CustomerWallet, entry *models.AdminAuditLog) error
}

type customerWalletRepository struct{}

// NewCustomerWalletRepository returns a CustomerWalletRepository backed by the
// default ORM connection.
func NewCustomerWalletRepository() CustomerWalletRepository {
	return &customerWalletRepository{}
}

func (r *customerWalletRepository) FindByAddress(chain, address string) (*models.CustomerWallet, error) {
	return first[models.CustomerWallet](facades.Orm().Query().Where("chain", chain).Where("address", address),
		"customer wallet repo: find by address")
}

func (r *customerWalletRepository) ListByCustomers(customerIDs []uint) ([]models.CustomerWallet, error) {
	wallets := []models.CustomerWallet{}
	if len(customerIDs) == 0 {
		return wallets, nil
	}
	if err := facades.Orm().Query().Where("customer_id IN ?", customerIDs).
		Order("created_at").Order("id").Get(&wallets); err != nil {
		return nil, fmt.Errorf("customer wallet repo: list by customers: %w", err)
	}
	return wallets, nil
}

func (r *customerWalletRepository) Create(wallet *models.CustomerWallet) error {
	if err := facades.Orm().Query().Create(wallet); err != nil {
		return fmt.Errorf("customer wallet repo: create: %w", taken(err))
	}
	return nil
}

func (r *customerWalletRepository) FindByCustomerAndID(customerID, walletID uint) (*models.CustomerWallet, error) {
	return first[models.CustomerWallet](facades.Orm().Query().Where("customer_id", customerID).Where("id", walletID),
		"customer wallet repo: find by customer and id")
}

func (r *customerWalletRepository) DeleteAudited(wallet *models.CustomerWallet, entry *models.AdminAuditLog) error {
	err := facades.Orm().Transaction(func(tx orm.Query) error {
		res, err := tx.Where("id", wallet.ID).Where("customer_id", wallet.CustomerID).Delete(&models.CustomerWallet{})
		if err != nil {
			return fmt.Errorf("delete: %w", err)
		}
		if res.RowsAffected == 0 {
			return ErrWalletGone
		}
		return createAuditLog(tx, entry)
	})
	if err != nil {
		return fmt.Errorf("customer wallet repo: release: %w", err)
	}
	return nil
}
