// Package bank is the admin's read of a customer's saved US bank accounts and
// where each stands at the providers. It reads the clear columns only: the
// name, the address and the full numbers stay encrypted, so nothing here can
// show them.
package bank

import (
	"fmt"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
)

// Beneficiary is one saved bank account with its provider mapping rows.
type Beneficiary struct {
	repositories.BankBeneficiaryRecord
	Accounts []models.CustomerBankBeneficiaryProviderAccount
}

// Service lists a customer's beneficiaries for support.
type Service struct {
	beneficiaries repositories.CustomerBankBeneficiaryRepository
	accounts      repositories.BankProviderAccountRepository
}

func NewService(
	beneficiaries repositories.CustomerBankBeneficiaryRepository, accounts repositories.BankProviderAccountRepository,
) *Service {
	return &Service{beneficiaries: beneficiaries, accounts: accounts}
}

// ListBeneficiaries is every beneficiary the customer ever saved, deleted ones
// included, each with every provider account row it ever had, superseded ones
// included: support reads the history, and a row stuck on echo_mismatch is how
// it finds an account that never became usable.
func (s *Service) ListBeneficiaries(customerID uint) ([]Beneficiary, error) {
	records, err := s.beneficiaries.ListByCustomerForAdmin(customerID)
	if err != nil {
		return nil, fmt.Errorf("admin bank: list beneficiaries: %w", err)
	}
	ids := make([]uint, len(records))
	for i := range records {
		ids[i] = records[i].ID
	}
	rows, err := s.accounts.ListByBeneficiaries(ids)
	if err != nil {
		return nil, fmt.Errorf("admin bank: list provider accounts: %w", err)
	}
	byBeneficiary := make(map[uint][]models.CustomerBankBeneficiaryProviderAccount, len(records))
	for _, row := range rows {
		byBeneficiary[row.BeneficiaryID] = append(byBeneficiary[row.BeneficiaryID], row)
	}
	out := make([]Beneficiary, len(records))
	for i := range records {
		out[i] = Beneficiary{BankBeneficiaryRecord: records[i], Accounts: byBeneficiary[records[i].ID]}
	}
	return out, nil
}
