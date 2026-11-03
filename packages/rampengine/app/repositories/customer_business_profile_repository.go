package repositories

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/goravel/framework/contracts/database/orm"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// errNoBusinessProfile is an application saved for a customer that holds no
// declaration: no draft was ever opened for it.
var errNoBusinessProfile = errors.New("business profile repo: no declaration to replace")

// CustomerBusinessProfileRepository is the persistence boundary for the
// company a business customer declared, encrypted at rest. The draft's first
// declaration is written with the customer (CustomerRepository.
// CreateBusinessDraft).
type CustomerBusinessProfileRepository interface {
	// FindByCustomers is the declaration of each of customerIDs that has one,
	// by customer id.
	FindByCustomers(customerIDs []uint) (map[uint]models.BusinessProfile, error)
	// SaveApplication replaces, in one transaction, the declaration of the
	// business customerID with profile and its people with people, matched
	// by LocalRef: one named again is updated, a new one added, one no longer
	// named removed (API contract §5.2). The tax identity and number stay the
	// draft's, whatever profile carries: the document a draft holds is never
	// swapped by an application.
	SaveApplication(customerID uint, profile models.BusinessProfile, people []models.BusinessPerson) error
	// ListPeople is the people the business's last application named, in the
	// order they were first named.
	ListPeople(customerID uint) ([]models.BusinessPerson, error)
	// FindForAdmin is the business's declaration, decrypted, and when it was
	// last written; nil when it holds none.
	FindForAdmin(customerID uint) (*AdminBusinessProfile, error)
}

// AdminBusinessProfile is a business's declaration as the admin reads it.
type AdminBusinessProfile struct {
	Profile   models.BusinessProfile
	UpdatedAt *carbon.DateTime
}

type customerBusinessProfileRepository struct{}

// NewCustomerBusinessProfileRepository returns a
// CustomerBusinessProfileRepository backed by the default ORM connection.
func NewCustomerBusinessProfileRepository() CustomerBusinessProfileRepository {
	return &customerBusinessProfileRepository{}
}

func (r *customerBusinessProfileRepository) FindByCustomers(customerIDs []uint) (map[uint]models.BusinessProfile, error) {
	out := make(map[uint]models.BusinessProfile, len(customerIDs))
	if len(customerIDs) == 0 {
		return out, nil
	}
	var rows []models.CustomerBusinessProfile
	if err := facades.Orm().Query().Where("customer_id IN ?", customerIDs).Get(&rows); err != nil {
		return nil, fmt.Errorf("business profile repo: find by customers: %w", err)
	}
	for i := range rows {
		profile, err := openBusinessProfile(rows[i].Profile)
		if err != nil {
			return nil, err
		}
		out[rows[i].CustomerID] = profile
	}
	return out, nil
}

// sealBusinessProfile encodes and encrypts a declaration for the profile
// column.
func sealBusinessProfile(profile models.BusinessProfile) (string, error) {
	encoded, err := json.Marshal(profile)
	if err != nil {
		return "", fmt.Errorf("business profile repo: encode: %w", err)
	}
	sealed, err := facades.Crypt().EncryptString(string(encoded))
	if err != nil {
		return "", fmt.Errorf("business profile repo: encrypt: %w", err)
	}
	return sealed, nil
}

// openBusinessProfile decrypts and decodes the declaration a row holds.
func openBusinessProfile(sealed string) (models.BusinessProfile, error) {
	var profile models.BusinessProfile
	plaintext, err := facades.Crypt().DecryptString(sealed)
	if err != nil {
		return profile, fmt.Errorf("business profile repo: decrypt: %w", err)
	}
	if err := json.Unmarshal([]byte(plaintext), &profile); err != nil {
		return profile, fmt.Errorf("business profile repo: decode: %w", err)
	}
	return profile, nil
}

func (r *customerBusinessProfileRepository) SaveApplication(customerID uint, profile models.BusinessProfile, people []models.BusinessPerson) error {
	err := facades.Orm().Transaction(func(tx orm.Query) error {
		row, err := first[models.CustomerBusinessProfile](tx.LockForUpdate().Where("customer_id", customerID),
			"business profile repo: lock declaration")
		if err != nil {
			return err
		}
		if row == nil {
			return errNoBusinessProfile
		}
		stored, err := openBusinessProfile(row.Profile)
		if err != nil {
			return err
		}
		profile.TaxIdentity, profile.TaxID = stored.TaxIdentity, stored.TaxID
		if row.Profile, err = sealBusinessProfile(profile); err != nil {
			return err
		}
		if err := tx.Save(row); err != nil {
			return fmt.Errorf("business profile repo: save declaration: %w", err)
		}
		return replacePeople(tx, customerID, people)
	})
	if err != nil {
		return fmt.Errorf("business profile repo: save application: %w", err)
	}
	return nil
}

// replacePeople makes the business's people people, matched by LocalRef.
func replacePeople(tx orm.Query, customerID uint, people []models.BusinessPerson) error {
	var rows []models.CustomerAssociatedPerson
	if err := tx.Where("customer_id", customerID).Get(&rows); err != nil {
		return fmt.Errorf("read people: %w", err)
	}
	byRef := make(map[string]*models.CustomerAssociatedPerson, len(rows))
	for i := range rows {
		byRef[rows[i].LocalRef] = &rows[i]
	}
	named := make([]string, 0, len(people))
	for _, person := range people {
		named = append(named, person.LocalRef)
		row, ok := byRef[person.LocalRef]
		if !ok {
			row = &models.CustomerAssociatedPerson{CustomerID: customerID, LocalRef: person.LocalRef}
		}
		if err := fillPerson(row, person); err != nil {
			return err
		}
		if err := tx.Save(row); err != nil {
			return fmt.Errorf("save person: %w", err)
		}
	}
	gone := tx.Where("customer_id", customerID)
	if len(named) > 0 {
		gone = gone.Where("local_ref NOT IN ?", named)
	}
	if _, err := gone.Delete(&models.CustomerAssociatedPerson{}); err != nil {
		return fmt.Errorf("remove people no longer named: %w", err)
	}
	return nil
}

// personDeclaration is what the profile column of a person encrypts.
type personDeclaration struct {
	Person models.KycProfile
	Title  string
}

// fillPerson writes person onto row: its columns, and its declaration sealed.
func fillPerson(row *models.CustomerAssociatedPerson, person models.BusinessPerson) error {
	roles, err := json.Marshal(person.Roles)
	if err != nil {
		return fmt.Errorf("encode roles: %w", err)
	}
	declaration, err := json.Marshal(personDeclaration{Person: person.Person, Title: person.Title})
	if err != nil {
		return fmt.Errorf("encode person: %w", err)
	}
	sealed, err := facades.Crypt().EncryptString(string(declaration))
	if err != nil {
		return fmt.Errorf("encrypt person: %w", err)
	}
	row.Roles, row.Profile, row.IndividualCustomerID = string(roles), sealed, person.IndividualCustomerID
	row.OwnershipPercent = nil
	if person.OwnershipPercent != "" {
		share := person.OwnershipPercent
		row.OwnershipPercent = &share
	}
	return nil
}

func (r *customerBusinessProfileRepository) ListPeople(customerID uint) ([]models.BusinessPerson, error) {
	var rows []models.CustomerAssociatedPerson
	if err := facades.Orm().Query().Where("customer_id", customerID).Order("id").Get(&rows); err != nil {
		return nil, fmt.Errorf("business profile repo: list people: %w", err)
	}
	people := make([]models.BusinessPerson, 0, len(rows))
	for i := range rows {
		person, err := openPerson(&rows[i])
		if err != nil {
			return nil, err
		}
		people = append(people, person)
	}
	return people, nil
}

// openPerson reads a person back from its row.
func openPerson(row *models.CustomerAssociatedPerson) (models.BusinessPerson, error) {
	person := models.BusinessPerson{ID: row.ID, LocalRef: row.LocalRef, IndividualCustomerID: row.IndividualCustomerID}
	if row.OwnershipPercent != nil {
		person.OwnershipPercent = *row.OwnershipPercent
	}
	if err := json.Unmarshal([]byte(row.Roles), &person.Roles); err != nil {
		return person, fmt.Errorf("business profile repo: decode roles: %w", err)
	}
	plaintext, err := facades.Crypt().DecryptString(row.Profile)
	if err != nil {
		return person, fmt.Errorf("business profile repo: decrypt person: %w", err)
	}
	var declaration personDeclaration
	if err := json.Unmarshal([]byte(plaintext), &declaration); err != nil {
		return person, fmt.Errorf("business profile repo: decode person: %w", err)
	}
	person.Person, person.Title = declaration.Person, declaration.Title
	return person, nil
}

func (r *customerBusinessProfileRepository) FindForAdmin(customerID uint) (*AdminBusinessProfile, error) {
	row, err := first[models.CustomerBusinessProfile](facades.Orm().Query().Where("customer_id", customerID),
		"business profile repo: find for admin")
	if err != nil || row == nil {
		return nil, err
	}
	profile, err := openBusinessProfile(row.Profile)
	if err != nil {
		return nil, err
	}
	return &AdminBusinessProfile{Profile: profile, UpdatedAt: row.UpdatedAt}, nil
}
