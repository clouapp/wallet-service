package repositories

import (
	"errors"
	"fmt"
	"time"

	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// ErrLinkConflict refuses a link write naming a customer when the stored link
// already names another one: two writers read it empty — an operator's
// reconciliation and a late replay, say — and the second would have written
// its customer over the first's. Nothing is written.
var ErrLinkConflict = errors.New("the link already names another customer")

// CustomerProviderKycRepository is the persistence boundary for per-provider
// compliance state.
type CustomerProviderKycRepository interface {
	// FindByCustomerAndProvider returns (nil, nil) when the customer has never
	// started KYC at that provider.
	FindByCustomerAndProvider(customerID uint, provider string) (*models.CustomerProviderKyc, error)
	ListByCustomer(customerID uint) ([]models.CustomerProviderKyc, error)
	// ListStale returns up to limit rows at one of statuses that have not been
	// read from their provider since before, oldest first. A row never read
	// counts from when it was last written. The rows are not marked (no
	// Business, no NeedsApplication): the caller refreshes each through the
	// service, which loads its own.
	ListStale(statuses []string, before time.Time, limit int) ([]models.CustomerProviderKyc, error)
	Create(kyc *models.CustomerProviderKyc) error
	Update(kyc *models.CustomerProviderKyc) error
	// UpdateIfStatus is Update for a writer that decided from a read: it writes
	// the row only while its stored status is still status, and reports
	// whether it did. A write that marks a row in flight, or takes it back off
	// that, is decided from the status it read — written unconditionally from
	// a stale copy, it undid whatever landed in between (a provider's final
	// refusal, on 30/09).
	UpdateIfStatus(kyc *models.CustomerProviderKyc, status string) (bool, error)
	// SaveWithLink creates (isNew) or updates the row and saves the customer
	// link it addresses in one transaction: either write alone leaves a row and
	// a link that disagree about which provider customer it is. A link unlinked
	// since it was read answers ErrIntegrationGone, writing neither.
	//
	// The row is updated only while its stored status is still status, as
	// UpdateIfStatus; it reports whether the row was written. The link is
	// written either way: the customer it names exists upstream whatever
	// landed on the row since it was read.
	//
	// A link naming a customer lands only on a stored link with no customer or
	// that same one; another answers ErrLinkConflict, writing neither.
	SaveWithLink(kyc *models.CustomerProviderKyc, isNew bool, link *models.CustomerProviderIntegration, status string) (bool, error)
	// UpdateRecorded is Update of an admin act on the row: the row and entry,
	// its audit row, land in one transaction. A row soft-deleted since it was
	// read answers ErrKycGone, writing and recording nothing.
	UpdateRecorded(kyc *models.CustomerProviderKyc, entry *models.AdminAuditLog) error
}

// ErrKycGone answers an admin write on a KYC row soft-deleted since it was
// read.
var ErrKycGone = errors.New("kyc row gone")

type customerProviderKycRepository struct{}

// NewCustomerProviderKycRepository returns a CustomerProviderKycRepository backed by
// the default ORM connection.
func NewCustomerProviderKycRepository() CustomerProviderKycRepository {
	return &customerProviderKycRepository{}
}

func (r *customerProviderKycRepository) FindByCustomerAndProvider(customerID uint, provider string) (*models.CustomerProviderKyc, error) {
	row, err := first[models.CustomerProviderKyc](facades.Orm().Query().
		Where("customer_id", customerID).
		Where("provider", provider), "customer provider kyc repo: find by customer+provider")
	if err != nil || row == nil {
		return row, err
	}
	if err := markBusiness(facades.Orm().Query(), customerID, row); err != nil {
		return nil, err
	}
	return row, nil
}

func (r *customerProviderKycRepository) ListByCustomer(customerID uint) ([]models.CustomerProviderKyc, error) {
	var kycs []models.CustomerProviderKyc
	if err := facades.Orm().Query().
		Where("customer_id", customerID).
		Order("provider asc").
		Find(&kycs); err != nil {
		return nil, fmt.Errorf("customer provider kyc repo: list by customer: %w", err)
	}
	if len(kycs) == 0 {
		return kycs, nil
	}
	marked := make([]*models.CustomerProviderKyc, len(kycs))
	for i := range kycs {
		marked[i] = &kycs[i]
	}
	if err := markBusiness(facades.Orm().Query(), customerID, marked...); err != nil {
		return nil, err
	}
	return kycs, nil
}

func (r *customerProviderKycRepository) ListStale(statuses []string, before time.Time, limit int) ([]models.CustomerProviderKyc, error) {
	if len(statuses) == 0 || limit <= 0 {
		return nil, nil
	}
	var rows []models.CustomerProviderKyc
	if err := facades.Orm().Query().
		WhereIn("kyc_status", statusArgs(statuses)).
		Where("COALESCE(last_synced_at, updated_at) < ?", before).
		Order("COALESCE(last_synced_at, updated_at) asc").
		Limit(limit).
		Find(&rows); err != nil {
		return nil, fmt.Errorf("customer provider kyc repo: list stale: %w", err)
	}
	return rows, nil
}

// statusArgs is the []any WhereIn takes.
func statusArgs(statuses []string) []any {
	args := make([]any, len(statuses))
	for i, status := range statuses {
		args[i] = status
	}
	return args
}

// markBusiness sets Business on rows of customerID when it is a business —
// a closed one's included, which the admin still reads: a row knows whose it
// is, so whatever reads its steps reads the company's, not a person's. q is
// the query the rows were read through: a row locked in a transaction is
// marked in it, on its connection.
func markBusiness(q orm.Query, customerID uint, rows ...*models.CustomerProviderKyc) error {
	count, err := q.Model(&models.Customer{}).WithTrashed().
		Where("id", customerID).Where("type", models.CustomerTypeBusiness).Count()
	if err != nil {
		return fmt.Errorf("customer provider kyc repo: read customer %d type: %w", customerID, err)
	}
	for _, row := range rows {
		row.Business = count > 0
	}
	return nil
}

func (r *customerProviderKycRepository) Create(kyc *models.CustomerProviderKyc) error {
	if err := facades.Orm().Query().Create(kyc); err != nil {
		return fmt.Errorf("customer provider kyc repo: create: %w", err)
	}
	return nil
}

func (r *customerProviderKycRepository) Update(kyc *models.CustomerProviderKyc) error {
	return r.UpdateTx(facades.Orm().Query(), kyc)
}

// SaveWithLink writes the link and the row together. A link soft-deleted
// since it was read — an admin unlink under a hosted link or a worker —
// answers ErrIntegrationGone and writes nothing: the row would otherwise be
// kept, or created, for a customer nothing links to.
func (r *customerProviderKycRepository) SaveWithLink(
	kyc *models.CustomerProviderKyc, isNew bool, link *models.CustomerProviderIntegration, status string,
) (bool, error) {
	var rowWritten bool
	err := facades.Orm().Transaction(func(tx orm.Query) error {
		if err := writeLinkOnce(tx, link); err != nil {
			return err
		}
		if isNew {
			rowWritten = true
			return tx.Create(kyc)
		}
		written, err := updateLiveKyc(tx.Where("kyc_status", status), kyc)
		rowWritten = written
		return err
	})
	if err != nil {
		return false, fmt.Errorf("customer provider kyc repo: save with link: %w", err)
	}
	return rowWritten, nil
}

// writeLinkOnce writes the link inside tx. Naming a customer, it is a
// compare-and-set on the stored one: the update matches only a link with no
// customer or with this one, so of two writers that read it empty only the
// first lands. A miss tells a link unlinked since (ErrIntegrationGone) from
// one that names another customer (ErrLinkConflict).
func writeLinkOnce(tx orm.Query, link *models.CustomerProviderIntegration) error {
	q := tx
	if link.ExternalCustomerID != nil && *link.ExternalCustomerID != "" {
		q = tx.Where("COALESCE(external_customer_id, '') IN ('', ?)", *link.ExternalCustomerID)
	}
	written, err := updateLiveLink(q, link)
	if err != nil {
		return err
	}
	if written {
		return nil
	}
	live, err := first[models.CustomerProviderIntegration](tx.Where("id", link.ID), "find link")
	if err != nil {
		return err
	}
	if live == nil {
		return ErrIntegrationGone
	}
	return ErrLinkConflict
}

// UpdateTx writes every column of the row to its live row, and nothing when
// the row was soft-deleted since it was read — an admin unlink under a
// refresh, a worker or a webhook. That write is a no-op, not an error: a
// worker must not stall or requeue over a row an operator removed. It never
// inserts, which Save did when no row matched, resurrecting the row.
func (r *customerProviderKycRepository) UpdateTx(q orm.Query, kyc *models.CustomerProviderKyc) error {
	if _, err := updateLiveKyc(q, kyc); err != nil {
		return fmt.Errorf("customer provider kyc repo: update: %w", err)
	}
	return nil
}

func (r *customerProviderKycRepository) UpdateIfStatus(kyc *models.CustomerProviderKyc, status string) (bool, error) {
	written, err := updateLiveKyc(facades.Orm().Query().Where("kyc_status", status), kyc)
	if err != nil {
		return false, fmt.Errorf("customer provider kyc repo: update if %s: %w", status, err)
	}
	return written, nil
}

func (r *customerProviderKycRepository) UpdateRecorded(kyc *models.CustomerProviderKyc, entry *models.AdminAuditLog) error {
	err := facades.Orm().Transaction(func(tx orm.Query) error {
		written, err := updateLiveKyc(tx, kyc)
		if err != nil {
			return err
		}
		if !written {
			return ErrKycGone
		}
		return createAuditLog(tx, entry)
	})
	if err != nil {
		return fmt.Errorf("customer provider kyc repo: update recorded: %w", err)
	}
	return nil
}

// updateLiveKyc writes every column of kyc to its row while it is live. The
// jsonb lists are written as empty lists when empty, never as an empty
// string the column would refuse.
func updateLiveKyc(q orm.Query, kyc *models.CustomerProviderKyc) (bool, error) {
	for _, list := range []*string{&kyc.Endorsements, &kyc.Requirements, &kyc.RejectionReasons,
		&kyc.RejectionCategories, &kyc.RejectionDetails} {
		if *list == "" {
			*list = "[]"
		}
	}
	if kyc.RawStatus == "" {
		kyc.RawStatus = "{}"
	}
	return updateLive(q, kyc)
}
