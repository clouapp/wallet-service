package repositories

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

type WhitelistEntryRepository interface {
	Create(entry *models.WhitelistEntry) error
	FindByWalletID(walletID uuid.UUID) ([]models.WhitelistEntry, error)
	FindByIDAndWallet(id, walletID uuid.UUID) (*models.WhitelistEntry, error)
	Delete(entry *models.WhitelistEntry) error
}

type whitelistEntryRepository struct{}

func NewWhitelistEntryRepository() WhitelistEntryRepository {
	return &whitelistEntryRepository{}
}

func (r *whitelistEntryRepository) Create(entry *models.WhitelistEntry) error {
	return facades.Orm().Query().Create(entry)
}

func (r *whitelistEntryRepository) FindByWalletID(walletID uuid.UUID) ([]models.WhitelistEntry, error) {
	var entries []models.WhitelistEntry
	err := facades.Orm().Query().
		Where("wallet_id = ?", walletID).
		Find(&entries)
	return entries, err
}

func (r *whitelistEntryRepository) FindByIDAndWallet(id, walletID uuid.UUID) (*models.WhitelistEntry, error) {
	var entry models.WhitelistEntry
	err := facades.Orm().Query().
		Where("id = ? AND wallet_id = ?", id, walletID).
		First(&entry)
	if err != nil {
		return nil, err
	}
	if entry.ID == uuid.Nil {
		return nil, nil
	}
	return &entry, nil
}

func (r *whitelistEntryRepository) Delete(entry *models.WhitelistEntry) error {
	_, err := facades.Orm().Query().Delete(entry)
	return err
}
