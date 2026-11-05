package repositories

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/goravel/framework/contracts/database/orm"
	ormmodel "github.com/goravel/framework/database/orm"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/support/keyedhash"
)

// ErrTaxIDHeld refuses moving a business past draft while another business
// past draft holds its document: the first approval of a document wins.
var ErrTaxIDHeld = errors.New("business document held by another customer")

// ErrCustomerGone is a write on a business customer no row holds.
var ErrCustomerGone = errors.New("customer not found")

// ErrBusinessLive refuses releasing the document of a business that still
// holds it at a provider: neither closed nor deleted.
var ErrBusinessLive = errors.New("business still live")

// ErrCustomerClosed refuses putting money in flight for a closed customer: a
// reservation that a close committed before (lockOpenCustomer).
var ErrCustomerClosed = errors.New("customer closed")

// errOwnerRoleMissing is a database the owner role was never seeded in: no
// account can be created without its customer, so this stops the sign-up
// instead of leaving an account that holds nothing.
var errOwnerRoleMissing = errors.New("the customer owner role is not seeded")

// CustomerRepository is the persistence boundary for customers, who hold the
// money (spec §3.1). The individual customer every account holds is written
// by UserRepository, in the account's own transaction; a business is written
// here.
type CustomerRepository interface {
	// FindByID is the live customer with id; (nil, nil) when there is none,
	// a deleted one included.
	FindByID(id uint) (*models.Customer, error)
	// ListByMember is every live customer userID is a member of: the
	// individual one first, then the businesses by creation. A deleted
	// customer is left out; a closed one is listed.
	ListByMember(userID uint) ([]models.Customer, error)
	// TaxIDHeld reports whether a business past draft holds the document
	// digest names (BusinessTaxIDDigest) — an active one, a closed one, or one
	// deleted after it left draft: the hold outlives both until an admin frees
	// it (spec §18.1). A draft holds nothing.
	TaxIDHeld(digest string) (bool, error)
	// TaxIDHeldByAnother is TaxIDHeld for a business other than customerID:
	// at a submission the business submitting, approved already, holds its
	// own document.
	TaxIDHeldByAnother(digest string, customerID uint) (bool, error)
	// ActivateBusiness turns the business customerID from draft to active,
	// its first approval, and reports whether it did: one active or closed
	// already is left as it is. ErrTaxIDHeld when another business past
	// draft holds its document, which leaves it a draft.
	ActivateBusiness(customerID uint) (bool, error)
	// FindDraftByMember is the oldest live business draft userID is a member
	// of holding the document digest names, or nil.
	FindDraftByMember(userID uint, digest string) (*models.Customer, error)
	// AdminPaginate is a page of the live customers filters selects, for the
	// admin's Clientes listing, and how many it selects in all.
	AdminPaginate(filters AdminCustomerListFilters) ([]models.Customer, int64, error)
	// FindByIDWithTrashed is FindByID with a deleted customer included.
	FindByIDWithTrashed(id uint) (*models.Customer, error)
	// CloseBusinessAudited closes the live business customerID and writes
	// entry, in one transaction, and reports whether it closed it: one closed
	// already is left as it is and records nothing. A draft's document digest
	// goes with it — a draft never held its document (spec §3.1), and a closed
	// one would start to. It refuses while money can still move for the
	// business (ErrVirtualAccountOpen, ErrTransactionOpen), read inside the
	// transaction after the row is taken. ErrCustomerGone when there is no
	// such business.
	CloseBusinessAudited(customerID uint, entry *models.AdminAuditLog) (bool, error)
	// ReleaseBusinessTaxIDAudited lets the document of the business
	// customerID go — a closed or a deleted one (spec §18.1) — writing entry
	// with it, and reports whether it held one. ErrBusinessLive for one still
	// in draft or active; ErrCustomerGone when there is no such business.
	ReleaseBusinessTaxIDAudited(customerID uint, entry *models.AdminAuditLog) (bool, error)
	// CreateBusinessDraft writes, in one transaction, a business customer in
	// draft — its id the next of customers_business_id_seq, its document's
	// digest taken from profile — userID's membership of it with the owner
	// role, and profile, encrypted.
	CreateBusinessDraft(userID uint, profile models.BusinessProfile) (*models.Customer, error)
}

// AdminCustomerListFilters scopes the admin's customers listing (KYB contract
// §12.2). A zero value does not filter.
type AdminCustomerListFilters struct {
	// Search is a customer id, part of a member's e-mail or handle, or a
	// company's CNPJ as typed — matched by its digest, never by its text.
	Search string
	Type   string
	Status string
	// Provider alone selects a customer holding a KYC row there; with
	// KycStatus, a row there in that status.
	Provider  string
	KycStatus string
	Sort      string
	Page      int
	PerPage   int
}

type customerRepository struct{}

// NewCustomerRepository returns a CustomerRepository backed by the default
// ORM connection.
func NewCustomerRepository() CustomerRepository {
	return &customerRepository{}
}

// BusinessTaxIDDigest is the digest a company's document is held by
// (customers.tax_id_hash, spec §3.1), taken of its canonical form
// (models.CanonicalBusinessTaxID). A CNPJ is digested bare, the way its Pix
// key is (pixkeys.HashKey): a company's off-ramp is refused unless the key it
// pays to digests to this (holder_mismatch, spec §9). Every other document
// carries its type and country, TYPE:COUNTRY:DOCUMENT, as a person's passport
// does: the same number from two countries is two companies, and the form is
// never a person's old COUNTRY:document.
func BusinessTaxIDDigest(identity models.TaxIdentity, taxID string) string {
	document := models.CanonicalBusinessTaxID(identity.Type, taxID)
	if identity.Type == models.TaxIDTypeCNPJ {
		return keyedhash.Hex(document)
	}
	return keyedhash.Hex(strings.ToUpper(identity.Type) + ":" + identity.Country + ":" + document)
}

func (r *customerRepository) FindByID(id uint) (*models.Customer, error) {
	return first[models.Customer](facades.Orm().Query().Where("id", id), "customer repo: find by id")
}

func (r *customerRepository) ListByMember(userID uint) ([]models.Customer, error) {
	var customers []models.Customer
	// type = 'business' is false for the individual customer, which sorts it
	// first; the businesses follow by creation.
	if err := facades.Orm().Query().
		Join("JOIN customer_members ON customer_members.customer_id = customers.id").
		Where("customer_members.user_id", userID).
		Order("customers.type = 'business'").Order("customers.created_at").Order("customers.id").
		Select("customers.*").
		Get(&customers); err != nil {
		return nil, fmt.Errorf("customer repo: list by member: %w", err)
	}
	return customers, nil
}

// customerSort is the listing's order: newest first unless asked.
var customerSort = sortable{
	columns: map[string]string{"created_at": "customers.created_at"},
	column:  "customers.created_at",
	dir:     "desc",
}

func (r *customerRepository) AdminPaginate(filters AdminCustomerListFilters) ([]models.Customer, int64, error) {
	q := facades.Orm().Query().Model(&models.Customer{})
	if filters.Type != "" {
		q = q.Where("customers.type", filters.Type)
	}
	if filters.Status != "" {
		q = q.Where("customers.status", filters.Status)
	}
	if filters.Search != "" {
		q = searchCustomers(q, filters.Search)
	}
	if filters.Provider != "" || filters.KycStatus != "" {
		q = holdingKyc(q, filters.Provider, filters.KycStatus)
	}
	column, dir := customerSort.parse(filters.Sort)
	q = q.OrderBy(column, dir).OrderBy("customers.id", dir)

	var customers []models.Customer
	var total int64
	if err := q.Paginate(filters.Page, filters.PerPage, &customers, &total); err != nil {
		return nil, 0, fmt.Errorf("customer repo: admin paginate: %w", err)
	}
	return customers, total, nil
}

// searchCustomers narrows q to the customers term names: by id, by a member's
// e-mail or handle — a deleted account's too — or by a CNPJ's digest.
func searchCustomers(q orm.Query, term string) orm.Query {
	term = strings.TrimSpace(term)
	like := likeContains(term)
	digest := BusinessTaxIDDigest(models.TaxIdentity{Type: models.TaxIDTypeCNPJ}, term)
	clause := `customers.tax_id_hash = ? OR EXISTS (SELECT 1 FROM customer_members m JOIN users u ON u.id = m.user_id
		WHERE m.customer_id = customers.id AND (u.email ILIKE ? OR u.handle ILIKE ?))`
	args := []any{digest, like, like}
	if id, err := strconv.ParseUint(term, 10, 64); err == nil {
		clause = "customers.id = ? OR " + clause
		args = append([]any{id}, args...)
	}
	return q.Where("("+clause+")", args...)
}

// holdingKyc narrows q to the customers holding a live KYC row at provider,
// in status; an empty one does not narrow.
func holdingKyc(q orm.Query, provider, status string) orm.Query {
	clause := "EXISTS (SELECT 1 FROM customer_provider_kycs k WHERE k.customer_id = customers.id AND k.deleted_at IS NULL"
	var args []any
	if provider != "" {
		clause += " AND k.provider = ?"
		args = append(args, provider)
	}
	if status != "" {
		clause += " AND k.kyc_status = ?"
		args = append(args, status)
	}
	return q.Where(clause+")", args...)
}

func (r *customerRepository) TaxIDHeld(digest string) (bool, error) {
	held, err := holdersOf(digest).Exists()
	if err != nil {
		return false, fmt.Errorf("customer repo: tax id held: %w", err)
	}
	return held, nil
}

func (r *customerRepository) TaxIDHeldByAnother(digest string, customerID uint) (bool, error) {
	held, err := holdersOf(digest).Where("id <> ?", customerID).Where("tenant_key = (SELECT tenant_key FROM customers WHERE id = ?)", customerID).Exists()
	if err != nil {
		return false, fmt.Errorf("customer repo: tax id held by another: %w", err)
	}
	return held, nil
}

// holdersOf is the businesses past draft holding the document digest names,
// deleted ones included (spec §18.1).
func holdersOf(digest string) orm.Query {
	return facades.Orm().Query().Model(&models.Customer{}).WithTrashed().
		Where("type", models.CustomerTypeBusiness).
		Where("status <> ?", models.CustomerStatusDraft).
		Where("tax_id_hash", digest)
}

func (r *customerRepository) ActivateBusiness(customerID uint) (bool, error) {
	res, err := facades.Orm().Query().Model(&models.Customer{}).
		Where("id", customerID).Where("type", models.CustomerTypeBusiness).
		Where("status", models.CustomerStatusDraft).
		Update("status", models.CustomerStatusActive)
	if err != nil {
		return false, fmt.Errorf("customer repo: activate business: %w", taken(err))
	}
	return res.RowsAffected > 0, nil
}

func (r *customerRepository) FindDraftByMember(userID uint, digest string) (*models.Customer, error) {
	return first[models.Customer](facades.Orm().Query().
		Join("JOIN customer_members ON customer_members.customer_id = customers.id").
		Where("customer_members.user_id", userID).
		Where("customers.type", models.CustomerTypeBusiness).
		Where("customers.status", models.CustomerStatusDraft).
		Where("customers.tax_id_hash", digest).
		Order("customers.id").
		Select("customers.*"), "customer repo: find draft by member")
}

func (r *customerRepository) CreateBusinessDraft(userID uint, profile models.BusinessProfile) (*models.Customer, error) {
	sealed, err := sealBusinessProfile(profile)
	if err != nil {
		return nil, err
	}
	digest := BusinessTaxIDDigest(profile.TaxIdentity, profile.TaxID)
	var customer *models.Customer
	err = facades.Orm().Transaction(func(tx orm.Query) error {
		// The user row FOR SHARE, as a reservation of money takes it: a
		// deletion that committed first is seen, one that comes later waits
		// and finds the user owning this business (refuseLastOwner).
		if err := lockLiveUser(tx, userID); err != nil {
			return err
		}
		id, err := nextBusinessID(tx)
		if err != nil {
			return err
		}
		customer = &models.Customer{
			Model: ormmodel.Model{ID: id},
			Type:  models.CustomerTypeBusiness, Status: models.CustomerStatusDraft, TaxIDHash: &digest,
		}
		if err := tx.Create(customer); err != nil {
			return fmt.Errorf("create business: %w", err)
		}
		if err := addOwner(tx, customer.ID, userID); err != nil {
			return err
		}
		if err := tx.Create(&models.CustomerBusinessProfile{CustomerID: customer.ID, Profile: sealed}); err != nil {
			return fmt.Errorf("create business profile: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("customer repo: create business draft: %w", err)
	}
	return customer, nil
}

// nextBusinessID takes the next id of the business range. Raw SQL: the query
// builder has no nextval.
func nextBusinessID(tx orm.Query) (uint, error) {
	var ids []uint
	if err := tx.Raw("SELECT nextval('customers_business_id_seq')").Scan(&ids); err != nil {
		return 0, fmt.Errorf("next business id: %w", err)
	}
	if len(ids) != 1 {
		return 0, errors.New("next business id: no value")
	}
	return ids[0], nil
}

// createIndividualCustomer writes the individual customer every account holds
// — its id is the user's — the user's membership of it and the owner role on
// that membership, through tx, the transaction that inserted the user (spec
// §5, F0). The id being the user's is what keeps every key already sent to a
// provider the same and lets a request without a customer header resolve its
// customer without a query.
func createIndividualCustomer(tx orm.Query, user *models.User) error {
	customer := &models.Customer{
		Model:  ormmodel.Model{ID: user.ID},
		Type:   models.CustomerTypeIndividual,
		Status: models.CustomerStatusActive,
	}
	if err := tx.Create(customer); err != nil {
		return fmt.Errorf("create individual customer: %w", err)
	}
	if err := addOwner(tx, customer.ID, user.ID); err != nil {
		return fmt.Errorf("create individual customer: %w", err)
	}
	return nil
}

// addOwner makes userID a member of customerID holding the owner role, through
// tx.
func addOwner(tx orm.Query, customerID, userID uint) error {
	member := &models.CustomerMember{CustomerID: customerID, UserID: userID}
	if err := tx.Create(member); err != nil {
		return fmt.Errorf("membership: %w", err)
	}
	owner, err := first[models.Role](tx.Where("name", models.CustomerOwnerRoleName).
		Where("guard", models.CustomerGuard), "find owner role")
	if err != nil {
		return err
	}
	if owner == nil {
		return errOwnerRoleMissing
	}
	return attachRole(tx, owner.ID, member)
}

func (r *customerRepository) FindByIDWithTrashed(id uint) (*models.Customer, error) {
	return first[models.Customer](facades.Orm().Query().WithTrashed().Where("id", id), "customer repo: find by id with trashed")
}

func (r *customerRepository) CloseBusinessAudited(customerID uint, entry *models.AdminAuditLog) (bool, error) {
	closed := false
	err := facades.Orm().Transaction(func(tx orm.Query) error {
		customer, err := lockBusiness(tx, customerID)
		if err != nil || customer.Status == models.CustomerStatusClosed {
			return err
		}
		// After the row is taken, not before: a reservation of money for the
		// company reads the row FOR SHARE (lockOpenCustomer), so one that got
		// there first has committed by now and is seen here, and one that
		// comes later waits and finds the company closed.
		if err := refuseOpen(
			func() (bool, error) { return (&customerVirtualAccountRepository{}).HasOpenByCustomerTx(tx, customerID) },
			func() (bool, error) { return (&transactionRepository{}).HasOpenByCustomerTx(tx, customerID) },
		); err != nil {
			return err
		}
		columns := map[string]any{"status": models.CustomerStatusClosed}
		if customer.Status == models.CustomerStatusDraft {
			columns["tax_id_hash"] = nil
		}
		if _, err := tx.Model(&models.Customer{}).Where("id", customerID).Update(columns); err != nil {
			return fmt.Errorf("close: %w", err)
		}
		closed = true
		return createAuditLog(tx, entry)
	})
	if err != nil {
		return false, fmt.Errorf("customer repo: close business: %w", err)
	}
	return closed, nil
}

func (r *customerRepository) ReleaseBusinessTaxIDAudited(customerID uint, entry *models.AdminAuditLog) (bool, error) {
	released := false
	err := facades.Orm().Transaction(func(tx orm.Query) error {
		customer, err := lockBusiness(tx.WithTrashed(), customerID)
		if err != nil {
			return err
		}
		if customer.Status != models.CustomerStatusClosed && !customer.DeletedAt.Valid {
			return ErrBusinessLive
		}
		if customer.TaxIDHash == nil {
			return nil
		}
		if _, err := tx.WithTrashed().Model(&models.Customer{}).Where("id", customerID).
			Update(map[string]any{"tax_id_hash": nil}); err != nil {
			return fmt.Errorf("release: %w", err)
		}
		released = true
		return createAuditLog(tx, entry)
	})
	if err != nil {
		return false, fmt.Errorf("customer repo: release business tax id: %w", err)
	}
	return released, nil
}

// lockOpenCustomer takes the row of customerID FOR SHARE in q's transaction,
// and answers ErrCustomerClosed for a closed customer. It is how a write that
// puts money in flight for a customer serializes with the customer's close, as
// lockLiveUser does with the account's deletion: FOR SHARE conflicts with the
// close's FOR UPDATE, so whichever commits first is seen by the other. A
// customer with no row is left to the foreign key.
func lockOpenCustomer(q orm.Query, customerID uint) error {
	customer, err := first[models.Customer](q.SharedLock().Where("id", customerID), "lock open customer")
	if err != nil {
		return err
	}
	if customer != nil && customer.Status == models.CustomerStatusClosed {
		return ErrCustomerClosed
	}
	return nil
}

// lockBusiness takes the business customerID's row FOR UPDATE through q, or
// answers ErrCustomerGone.
func lockBusiness(q orm.Query, customerID uint) (*models.Customer, error) {
	customer, err := first[models.Customer](q.LockForUpdate().Where("id", customerID).
		Where("type", models.CustomerTypeBusiness), "lock business")
	if err != nil {
		return nil, err
	}
	if customer == nil {
		return nil, ErrCustomerGone
	}
	return customer, nil
}
