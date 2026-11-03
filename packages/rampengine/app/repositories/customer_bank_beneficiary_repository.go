package repositories

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// ErrBankBeneficiaryNotFound answers an external id that does not exist, was
// deleted, or belongs to another customer; the three are one on purpose.
var ErrBankBeneficiaryNotFound = errors.New("bank beneficiary not found")

// ErrBankBeneficiaryDuplicate answers a routing and account number the
// customer already saved (the fingerprint unique among undeleted rows).
var ErrBankBeneficiaryDuplicate = errors.New("duplicate bank beneficiary")

// BankBeneficiaryWrite is a beneficiary as the service hands it over:
// validated, normalized, with its fingerprint and last4s already derived.
type BankBeneficiaryWrite struct {
	Label, AccountClass, AccountType, Relationship          string
	AccountLast4, RoutingLast4, Fingerprint                 string
	BeneficiaryName, RoutingNumber, AccountNumber           string
	AddressLine1, AddressLine2, City, StateCode, PostalCode string
	BusinessIndustry                                        string
}

// sameProviderFields reports whether w sends the provider what current does.
// Everything but the label is sent (E4), so only a label edit keeps the
// revision.
func (w BankBeneficiaryWrite) sameProviderFields(current BankBeneficiaryWrite) bool {
	w.Label, current.Label = "", ""
	return w == current
}

// BankBeneficiaryRecord is a decrypted row.
type BankBeneficiaryRecord struct {
	ID         uint
	ExternalID string
	CustomerID uint
	Revision   int
	CreatedAt  time.Time
	UpdatedAt  time.Time
	// DeletedAt is set only on what ListByCustomerForAdmin reads: the moment
	// a soft-deleted beneficiary was removed, nil for a live one.
	DeletedAt *time.Time
	BankBeneficiaryWrite
}

// CustomerBankBeneficiaryRepository is the encrypted store of a customer's
// saved US bank accounts.
type CustomerBankBeneficiaryRepository interface {
	ListByCustomer(customerID uint) ([]BankBeneficiaryRecord, error)
	// ListByCustomerForAdmin reads the customer's beneficiaries, soft-deleted
	// ones included, from the clear columns only: nothing is decrypted, and
	// the encrypted ones are not even selected. The returned records carry no
	// name, address or number beyond the last four digits.
	ListByCustomerForAdmin(customerID uint) ([]BankBeneficiaryRecord, error)
	FindByExternalID(customerID uint, externalID string) (*BankBeneficiaryRecord, error)
	Create(customerID uint, w BankBeneficiaryWrite) (*BankBeneficiaryRecord, error)
	// Update replaces the beneficiary, bumping its revision when a field the
	// provider receives changed.
	Update(customerID uint, externalID string, w BankBeneficiaryWrite) (*BankBeneficiaryRecord, error)
	Delete(customerID uint, externalID string) error
}

type customerBankBeneficiaryRepository struct{}

func NewCustomerBankBeneficiaryRepository() CustomerBankBeneficiaryRepository {
	return &customerBankBeneficiaryRepository{}
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// seal writes w's columns into row, encrypting the sensitive ones.
func sealBankBeneficiary(row *models.CustomerBankBeneficiary, w BankBeneficiaryWrite) error {
	enc := func(dst *string, v, name string) error {
		c, err := encryptField(v)
		if err != nil {
			return fmt.Errorf("bank beneficiary repo: encrypt %s: %w", name, err)
		}
		*dst = c
		return nil
	}
	encOpt := func(dst **string, v, name string) error {
		c, err := encryptOptionalField(optional(v))
		if err != nil {
			return fmt.Errorf("bank beneficiary repo: encrypt %s: %w", name, err)
		}
		*dst = c
		return nil
	}
	row.Label, row.AccountClass, row.AccountType, row.Relationship = w.Label, w.AccountClass, w.AccountType, w.Relationship
	row.AccountLast4, row.RoutingLast4, row.Fingerprint = w.AccountLast4, w.RoutingLast4, w.Fingerprint
	for _, f := range []struct {
		dst  *string
		v    string
		name string
	}{
		{&row.RoutingNumber, w.RoutingNumber, "routing_number"},
		{&row.AccountNumber, w.AccountNumber, "account_number"},
		{&row.BeneficiaryName, w.BeneficiaryName, "beneficiary_name"},
		{&row.AddressLine1, w.AddressLine1, "address_line_1"},
		{&row.City, w.City, "city"},
		{&row.StateCode, w.StateCode, "state_code"},
		{&row.PostalCode, w.PostalCode, "postal_code"},
	} {
		if err := enc(f.dst, f.v, f.name); err != nil {
			return err
		}
	}
	if err := encOpt(&row.AddressLine2, w.AddressLine2, "address_line_2"); err != nil {
		return err
	}
	return encOpt(&row.BusinessIndustry, w.BusinessIndustry, "business_industry")
}

func openBankBeneficiary(row *models.CustomerBankBeneficiary) (*BankBeneficiaryRecord, error) {
	rec := &BankBeneficiaryRecord{
		ID: row.ID, ExternalID: row.ExternalID, CustomerID: row.CustomerID, Revision: row.Revision,
	}
	if row.CreatedAt != nil {
		rec.CreatedAt = row.CreatedAt.StdTime()
	}
	if row.UpdatedAt != nil {
		rec.UpdatedAt = row.UpdatedAt.StdTime()
	}
	w := &rec.BankBeneficiaryWrite
	w.Label, w.AccountClass, w.AccountType, w.Relationship = row.Label, row.AccountClass, row.AccountType, row.Relationship
	w.AccountLast4, w.RoutingLast4, w.Fingerprint = row.AccountLast4, row.RoutingLast4, row.Fingerprint
	for _, f := range []struct {
		dst  *string
		v    string
		name string
	}{
		{&w.RoutingNumber, row.RoutingNumber, "routing_number"},
		{&w.AccountNumber, row.AccountNumber, "account_number"},
		{&w.BeneficiaryName, row.BeneficiaryName, "beneficiary_name"},
		{&w.AddressLine1, row.AddressLine1, "address_line_1"},
		{&w.City, row.City, "city"},
		{&w.StateCode, row.StateCode, "state_code"},
		{&w.PostalCode, row.PostalCode, "postal_code"},
	} {
		plain, err := decryptField(f.v)
		if err != nil {
			return nil, fmt.Errorf("bank beneficiary repo: decrypt %s: %w", f.name, err)
		}
		*f.dst = plain
	}
	line2, err := decryptOptionalField(row.AddressLine2)
	if err != nil {
		return nil, fmt.Errorf("bank beneficiary repo: decrypt address_line_2: %w", err)
	}
	industry, err := decryptOptionalField(row.BusinessIndustry)
	if err != nil {
		return nil, fmt.Errorf("bank beneficiary repo: decrypt business_industry: %w", err)
	}
	w.AddressLine2, w.BusinessIndustry = deref(line2), deref(industry)
	return rec, nil
}

func (r *customerBankBeneficiaryRepository) owned(q orm.Query, customerID uint, externalID string) (*models.CustomerBankBeneficiary, error) {
	if _, err := uuid.Parse(externalID); err != nil {
		return nil, ErrBankBeneficiaryNotFound
	}
	row, err := first[models.CustomerBankBeneficiary](
		q.Where("customer_id", customerID).Where("external_id", externalID), "bank beneficiary repo: find owned")
	if err == nil && row == nil {
		return nil, ErrBankBeneficiaryNotFound
	}
	return row, err
}

func (r *customerBankBeneficiaryRepository) ListByCustomer(customerID uint) ([]BankBeneficiaryRecord, error) {
	var rows []models.CustomerBankBeneficiary
	if err := facades.Orm().Query().Where("customer_id", customerID).Order("created_at asc").Order("id asc").Find(&rows); err != nil {
		return nil, fmt.Errorf("bank beneficiary repo: list: %w", err)
	}
	out := make([]BankBeneficiaryRecord, 0, len(rows))
	for i := range rows {
		rec, err := openBankBeneficiary(&rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, *rec)
	}
	return out, nil
}

func (r *customerBankBeneficiaryRepository) ListByCustomerForAdmin(customerID uint) ([]BankBeneficiaryRecord, error) {
	var rows []models.CustomerBankBeneficiary
	if err := facades.Orm().Query().WithTrashed().
		Select("id", "external_id", "customer_id", "label", "account_class", "account_type", "relationship",
			"account_last4", "routing_last4", "revision", "created_at", "updated_at", "deleted_at").
		Where("customer_id", customerID).Order("created_at asc").Order("id asc").Find(&rows); err != nil {
		return nil, fmt.Errorf("bank beneficiary repo: list for admin: %w", err)
	}
	out := make([]BankBeneficiaryRecord, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		rec := BankBeneficiaryRecord{ID: row.ID, ExternalID: row.ExternalID, CustomerID: row.CustomerID, Revision: row.Revision}
		if row.CreatedAt != nil {
			rec.CreatedAt = row.CreatedAt.StdTime()
		}
		if row.UpdatedAt != nil {
			rec.UpdatedAt = row.UpdatedAt.StdTime()
		}
		if row.DeletedAt.Valid {
			deleted := row.DeletedAt.Time
			rec.DeletedAt = &deleted
		}
		rec.Label, rec.AccountClass, rec.AccountType, rec.Relationship = row.Label, row.AccountClass, row.AccountType, row.Relationship
		rec.AccountLast4, rec.RoutingLast4 = row.AccountLast4, row.RoutingLast4
		out = append(out, rec)
	}
	return out, nil
}

func (r *customerBankBeneficiaryRepository) FindByExternalID(customerID uint, externalID string) (*BankBeneficiaryRecord, error) {
	row, err := r.owned(facades.Orm().Query(), customerID, externalID)
	if err != nil {
		return nil, err
	}
	return openBankBeneficiary(row)
}

func (r *customerBankBeneficiaryRepository) Create(customerID uint, w BankBeneficiaryWrite) (*BankBeneficiaryRecord, error) {
	row := &models.CustomerBankBeneficiary{ExternalID: uuid.NewString(), CustomerID: customerID, Revision: 1}
	if err := sealBankBeneficiary(row, w); err != nil {
		return nil, err
	}
	if err := facades.Orm().Query().Create(row); err != nil {
		if isUniqueViolation(err) {
			return nil, ErrBankBeneficiaryDuplicate
		}
		return nil, fmt.Errorf("bank beneficiary repo: create: %w", err)
	}
	return openBankBeneficiary(row)
}

func (r *customerBankBeneficiaryRepository) Update(customerID uint, externalID string, w BankBeneficiaryWrite) (*BankBeneficiaryRecord, error) {
	var out *BankBeneficiaryRecord
	err := facades.Orm().Transaction(func(tx orm.Query) error {
		row, err := r.owned(tx.LockForUpdate(), customerID, externalID)
		if err != nil {
			return err
		}
		current, err := openBankBeneficiary(row)
		if err != nil {
			return err
		}
		if !w.sameProviderFields(current.BankBeneficiaryWrite) {
			row.Revision++
		}
		if err := sealBankBeneficiary(row, w); err != nil {
			return err
		}
		if err := tx.Save(row); err != nil {
			if isUniqueViolation(err) {
				return ErrBankBeneficiaryDuplicate
			}
			return fmt.Errorf("bank beneficiary repo: update: %w", err)
		}
		out, err = openBankBeneficiary(row)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (r *customerBankBeneficiaryRepository) Delete(customerID uint, externalID string) error {
	row, err := r.owned(facades.Orm().Query(), customerID, externalID)
	if err != nil {
		return err
	}
	if _, err := facades.Orm().Query().Delete(row); err != nil {
		return fmt.Errorf("bank beneficiary repo: delete: %w", err)
	}
	return nil
}

// PurgeByCustomerTx removes every beneficiary of the customer for good,
// soft-deleted ones included, through tx: the account deletion that takes the
// user's individual customer with it (decision D4).
func (r *customerBankBeneficiaryRepository) PurgeByCustomerTx(tx orm.Query, customerID uint) error {
	if _, err := tx.WithTrashed().Where("customer_id", customerID).ForceDelete(&models.CustomerBankBeneficiary{}); err != nil {
		return fmt.Errorf("bank beneficiary repo: purge: %w", err)
	}
	return nil
}
