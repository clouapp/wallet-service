package repositories

import (
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// ErrProviderAccountStale is a live mapping row that appeared or changed
// since it was read; the caller reads again under its lock.
var ErrProviderAccountStale = errors.New("bank provider account: changed since read")

type BankProviderAccountRepository interface {
	FindCurrent(beneficiaryID uint, provider, rail string) (*models.CustomerBankBeneficiaryProviderAccount, error)
	CreatePending(row *models.CustomerBankBeneficiaryProviderAccount) error
	Save(row *models.CustomerBankBeneficiaryProviderAccount) error
	Supersede(row *models.CustomerBankBeneficiaryProviderAccount) error
	ListByBeneficiaries(beneficiaryIDs []uint) ([]models.CustomerBankBeneficiaryProviderAccount, error)
}

type bankProviderAccountRepository struct{}

func NewBankProviderAccountRepository() BankProviderAccountRepository {
	return &bankProviderAccountRepository{}
}

func (r *bankProviderAccountRepository) FindCurrent(beneficiaryID uint, provider, rail string) (*models.CustomerBankBeneficiaryProviderAccount, error) {
	return first[models.CustomerBankBeneficiaryProviderAccount](facades.Orm().Query().
		Where("beneficiary_id", beneficiaryID).Where("provider", provider).Where("rail", rail).
		Where("status <> ?", models.ProviderAccountSuperseded), "bank provider account repo: find current")
}

func (r *bankProviderAccountRepository) CreatePending(row *models.CustomerBankBeneficiaryProviderAccount) error {
	if row.ExternalID == "" {
		row.ExternalID = uuid.NewString()
	}
	if err := facades.Orm().Query().Create(row); err != nil {
		if isUniqueViolation(err) {
			return ErrProviderAccountStale
		}
		return fmt.Errorf("bank provider account repo: create: %w", err)
	}
	return nil
}

func (r *bankProviderAccountRepository) Save(row *models.CustomerBankBeneficiaryProviderAccount) error {
	if err := facades.Orm().Query().Save(row); err != nil {
		return fmt.Errorf("bank provider account repo: save: %w", err)
	}
	return nil
}

func (r *bankProviderAccountRepository) Supersede(row *models.CustomerBankBeneficiaryProviderAccount) error {
	if _, err := facades.Orm().Query().Model(&models.CustomerBankBeneficiaryProviderAccount{}).
		Where("id", row.ID).Where("status <> ?", models.ProviderAccountSuperseded).
		Update("status", models.ProviderAccountSuperseded); err != nil {
		return fmt.Errorf("bank provider account repo: supersede: %w", err)
	}
	row.Status = models.ProviderAccountSuperseded
	return nil
}

func (r *bankProviderAccountRepository) ListByBeneficiaries(ids []uint) ([]models.CustomerBankBeneficiaryProviderAccount, error) {
	var rows []models.CustomerBankBeneficiaryProviderAccount
	if len(ids) == 0 {
		return rows, nil
	}
	if err := facades.Orm().Query().WhereIn("beneficiary_id", anyUints(ids)).
		Order("beneficiary_id asc").Order("rail asc").Order("created_at asc").Find(&rows); err != nil {
		return nil, fmt.Errorf("bank provider account repo: list: %w", err)
	}
	return rows, nil
}

func anyUints(values []uint) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}
