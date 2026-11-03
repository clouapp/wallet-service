package bank

import (
	"fmt"

	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/bank"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
)

// The repository's errors, so errors.Is matches them through the wrapping.
var (
	ErrBankBeneficiaryNotFound  = repositories.ErrBankBeneficiaryNotFound
	ErrBankBeneficiaryDuplicate = repositories.ErrBankBeneficiaryDuplicate
)

// Service is the customer's bank beneficiary book. It never calls a provider:
// registering an account at one is E4's, at quote time.
type Service struct {
	repo repositories.CustomerBankBeneficiaryRepository
}

func NewService(repo repositories.CustomerBankBeneficiaryRepository) *Service {
	return &Service{repo: repo}
}

func (s *Service) List(customerID uint) ([]dto.BeneficiaryRecordDTO, error) {
	list, err := s.repo.ListByCustomer(customerID)
	if err != nil {
		return nil, fmt.Errorf("bank beneficiaries: list: %w", err)
	}
	out := make([]dto.BeneficiaryRecordDTO, 0, len(list))
	for i := range list {
		out = append(out, recordDTO(&list[i]))
	}
	return out, nil
}

func (s *Service) Get(customerID uint, externalID string) (*dto.BeneficiaryRecordDTO, error) {
	rec, err := s.repo.FindByExternalID(customerID, externalID)
	if err != nil {
		return nil, fmt.Errorf("bank beneficiaries: get: %w", err)
	}
	return recordDTOPtr(rec), nil
}

// GetForPayout answers the beneficiary a bank payout links to: what Get
// answers and the row's internal id, which the order references. Only the
// ramps service calls it.
func (s *Service) GetForPayout(customerID uint, externalID string) (*dto.PayoutBeneficiaryDTO, error) {
	rec, err := s.repo.FindByExternalID(customerID, externalID)
	if err != nil {
		return nil, fmt.Errorf("bank beneficiaries: get for payout: %w", err)
	}
	return &dto.PayoutBeneficiaryDTO{ID: rec.ID, BeneficiaryRecordDTO: recordDTO(rec)}, nil
}

func (s *Service) Create(customerID uint, in dto.BeneficiaryDTO) (*dto.BeneficiaryRecordDTO, error) {
	w, err := prepare(in)
	if err != nil {
		return nil, err
	}
	rec, err := s.repo.Create(customerID, w)
	if err != nil {
		return nil, fmt.Errorf("bank beneficiaries: create: %w", err)
	}
	return recordDTOPtr(rec), nil
}

func (s *Service) Update(customerID uint, externalID string, in dto.BeneficiaryDTO) (*dto.BeneficiaryRecordDTO, error) {
	w, err := prepare(in)
	if err != nil {
		return nil, err
	}
	rec, err := s.repo.Update(customerID, externalID, w)
	if err != nil {
		return nil, fmt.Errorf("bank beneficiaries: update: %w", err)
	}
	return recordDTOPtr(rec), nil
}

func (s *Service) Delete(customerID uint, externalID string) error {
	if err := s.repo.Delete(customerID, externalID); err != nil {
		return fmt.Errorf("bank beneficiaries: delete: %w", err)
	}
	return nil
}

// prepare normalizes and validates in, and derives what the store compares
// and shows: the fingerprint and the two last4s.
func prepare(in dto.BeneficiaryDTO) (repositories.BankBeneficiaryWrite, error) {
	n := Normalize(in)
	if err := Validate(n); err != nil {
		return repositories.BankBeneficiaryWrite{}, err
	}
	return repositories.BankBeneficiaryWrite{
		Label: n.Label, AccountClass: n.AccountClass, AccountType: n.AccountType, Relationship: n.Relationship,
		AccountLast4: Last4(n.AccountNumber), RoutingLast4: Last4(n.RoutingNumber),
		Fingerprint:     Fingerprint(n.RoutingNumber, n.AccountNumber),
		BeneficiaryName: n.BeneficiaryName, RoutingNumber: n.RoutingNumber, AccountNumber: n.AccountNumber,
		AddressLine1: n.AddressLine1, AddressLine2: n.AddressLine2, City: n.City,
		StateCode: n.StateCode, PostalCode: n.PostalCode, BusinessIndustry: n.BusinessIndustry,
	}, nil
}

// recordDTO is what leaves the service of a stored beneficiary: the clear
// fields and the last four digits, never the full numbers or the fingerprint.
func recordDTO(rec *repositories.BankBeneficiaryRecord) dto.BeneficiaryRecordDTO {
	return dto.BeneficiaryRecordDTO{
		ExternalID: rec.ExternalID, Revision: rec.Revision, CreatedAt: rec.CreatedAt, UpdatedAt: rec.UpdatedAt,
		Label: rec.Label, AccountClass: rec.AccountClass, AccountType: rec.AccountType, Relationship: rec.Relationship,
		AccountLast4: rec.AccountLast4, RoutingLast4: rec.RoutingLast4, BeneficiaryName: rec.BeneficiaryName,
		AddressLine1: rec.AddressLine1, AddressLine2: rec.AddressLine2, City: rec.City,
		StateCode: rec.StateCode, PostalCode: rec.PostalCode, BusinessIndustry: rec.BusinessIndustry,
	}
}

func recordDTOPtr(rec *repositories.BankBeneficiaryRecord) *dto.BeneficiaryRecordDTO {
	out := recordDTO(rec)
	return &out
}
