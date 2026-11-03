package repositories

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// ErrIntegrationGone answers a write on a link soft-deleted since it was
// read: an unlink happened underneath the writer.
var ErrIntegrationGone = errors.New("integration gone")

// ErrVirtualAccountOpen refuses an unlink while the user holds a standing
// account at the provider that is active or being provisioned.
var ErrVirtualAccountOpen = errors.New("virtual account open")

// ErrTransactionOpen refuses an unlink while a transaction at the provider
// has not reached a terminal status.
var ErrTransactionOpen = errors.New("transaction open")

// ErrExternalCustomerLinkedElsewhere refuses a link to a provider-side customer
// another live link already holds: one provider customer is one XIP customer.
var ErrExternalCustomerLinkedElsewhere = errors.New("customer linked to another account")

// CustomerProviderIntegrationRepository is the persistence boundary for the
// link between a XIP customer and its customer at a provider.
type CustomerProviderIntegrationRepository interface {
	// FindByCustomerAndProvider returns (nil, nil) when the customer has never
	// been linked to that provider.
	FindByCustomerAndProvider(customerID uint, provider string) (*models.CustomerProviderIntegration, error)
	// FindByCustomerIDsAndProvider loads every link for the given customers at
	// one provider in a single query. The sync sweep uses it so a batch of
	// accounts does not re-ask FindByCustomerAndProvider per row.
	FindByCustomerIDsAndProvider(customerIDs []uint, provider string) ([]models.CustomerProviderIntegration, error)
	// FindByExternalCustomerID resolves a provider-side customer id back to the
	// local link, which is how an inbound webhook finds its customer.
	FindByExternalCustomerID(provider, externalCustomerID string) (*models.CustomerProviderIntegration, error)
	ListByCustomer(customerID uint) ([]models.CustomerProviderIntegration, error)
	Create(integration *models.CustomerProviderIntegration) error
	// Update writes every column of the link to its live row. A link
	// soft-deleted since it was read is not written — Save would have
	// inserted it back — and answers ErrIntegrationGone.
	Update(integration *models.CustomerProviderIntegration) error
	// Unlink forgets the customer's link at provider in one transaction: it
	// locks the live link (ErrIntegrationGone when there is none — a second
	// unlink), refuses while money can still move there (ErrVirtualAccountOpen,
	// ErrTransactionOpen), soft-deletes the link and the KYC row (already
	// cleared by the caller; nil when there is none), deletes the payout
	// destinations at that provider and writes the audit row record builds
	// from the link it deleted. A failure writes nothing.
	Unlink(customerID uint, provider string, kyc *models.CustomerProviderKyc,
		record func(link *models.CustomerProviderIntegration) *models.AdminAuditLog) error
	// DetachCustomer forgets the provider-side customer on link and writes the
	// customer's reset KYC row, in one transaction: the retry of a provider
	// whose KYC data cannot be changed, which takes a brand-new provider-side
	// customer. The link goes back to pending with no customer id, the
	// off-ramp destinations at that provider — registered against the
	// provider-side customer being left — are deleted, as a relink to another
	// id deletes them, and the provider-side customer left is recorded as
	// detached (IsDetachedCustomer), so the events it keeps sending can be
	// told from those of one still being linked.
	//
	// Only the provider-side customer the caller read is detached: a link
	// naming another one since answers ErrLinkConflict, one unlinked since
	// ErrIntegrationGone, and one with a submission intent still active —
	// a create that may be linking a customer right now — answers
	// ErrActiveKycIntentExists. Money still able to move at the provider
	// refuses it as it refuses Unlink (ErrVirtualAccountOpen,
	// ErrTransactionOpen). reset is written only while the stored row still
	// holds the status and the rejection categories of read, the row as the
	// decision was taken from: when it does not, nothing is written at all and
	// it reports false. On success link holds what was written.
	DetachCustomer(link *models.CustomerProviderIntegration, reset, read *models.CustomerProviderKyc) (bool, error)
	// IsDetachedCustomer reports whether DetachCustomer ever detached the
	// provider-side customer externalCustomerID at provider.
	IsDetachedCustomer(provider, externalCustomerID string) (bool, error)
	// AdminPaginate is the cross-holder compliance explorer. Rows are anchored
	// on the integration; Kyc is nil when the holder linked but never started
	// KYC. A holder no provider row names yet is a row of its own, with no
	// integration, only when KycStatus asks for it (AdminKycNoProvider).
	AdminPaginate(filters AdminKycListFilters) ([]AdminKycRow, int64, error)
	// FindByIDForAdmin loads one integration with its user and optional KYC row.
	FindByIDForAdmin(id uint) (*AdminKycRow, error)
	// AdminUnclassifiedRejections lists the explorer rows whose KYC is
	// rejected with no category: whether such a row is terminal depends on
	// what is left to send it, which only the KYC service can tell
	// (kycsvc.Terminal). Its answer comes back as TerminalKycIDs.
	AdminUnclassifiedRejections() ([]AdminKycRow, error)
}

// AdminKycNoProvider is the explorer's KycStatus for the holders no provider
// row names yet — no live link anywhere: a person who never started KYC, a
// company in draft. They are most holders, so the explorer lists them only
// when asked.
const AdminKycNoProvider = "no_provider"

// AdminKycListFilters scopes the admin compliance explorer. Empty string / zero /
// nil fields are ignored by AdminPaginate.
type AdminKycListFilters struct {
	Provider string
	// KycStatus is one of models.AllKycStatuses, or AdminKycNoProvider.
	KycStatus string
	// CustomerType and CustomerStatus are the holder's (models.CustomerType*,
	// models.CustomerStatus*).
	CustomerType      string
	CustomerStatus    string
	TosStatus         string
	CollectionMode    string
	IntegrationStatus string
	Rail              string
	Search            string // matched against handle, email, and external_customer_id
	SyncedBefore      *time.Time
	// RejectionCategory is one of models.KycRejectionCategories the row carries.
	RejectionCategory string
	// Terminal and HasError are tri-state: nil does not filter.
	Terminal *bool
	HasError *bool
	// TerminalKycIDs are the unclassified rejections (see
	// AdminUnclassifiedRejections) the KYC service found terminal; Terminal
	// counts them beside the rows a terminal category decides.
	TerminalKycIDs []uint
	Page           int
	PerPage        int
	Sort           string // last_synced_at|-last_synced_at|updated_at|-updated_at|created_at|-created_at
}

// AdminKycRow is one explorer row: a holder's link at one provider and its
// compliance state there, or — Integration nil — a holder no provider row
// names yet.
type AdminKycRow struct {
	Integration *models.CustomerProviderIntegration
	Kyc         *models.CustomerProviderKyc
	// Customer is the holder; User is a person's live account, nil for a
	// company.
	Customer *models.Customer
	User     *models.User
}

// adminKycKey is one explorer row as the page query reads it: its link, or
// none, and its holder.
type adminKycKey struct {
	IntegrationID *uint
	CustomerID    uint
}

// kycPair is a (customer_id, provider) tuple used for batch KYC lookups.
// Extracted from the anonymous struct that AdminPaginate used to repeat inline.
type kycPair struct {
	customerID uint
	provider   string
}

// adminKycBaseSQL is the shared FROM + JOIN backbone for AdminPaginate's count
// and ID queries. The four deleted_at guards are co-located here so a future
// filter added to buildAdminKycWhere cannot accidentally omit one.
//
// The explorer lists every live customer: a person while its account is live
// — the user joined is the one whose id the customer took — and a business,
// which joins no user. Each of its live links is a row; a customer with none
// is one row with no link, which buildAdminKycWhere keeps out unless asked.
const adminKycBaseSQL = `FROM customers c
	LEFT JOIN users u ON u.id = c.id AND c.type = 'individual' AND u.deleted_at IS NULL
	LEFT JOIN customer_provider_integrations i ON i.customer_id = c.id AND i.deleted_at IS NULL
	LEFT JOIN customer_provider_kycs k ON k.customer_id = i.customer_id AND k.provider = i.provider AND k.deleted_at IS NULL
	WHERE c.deleted_at IS NULL AND (c.type = 'business' OR u.id IS NOT NULL)`

type customerProviderIntegrationRepository struct{ environment string }

// NewCustomerProviderIntegrationRepository returns a
// CustomerProviderIntegrationRepository backed by the default ORM connection.
func NewCustomerProviderIntegrationRepository() CustomerProviderIntegrationRepository {
	return &customerProviderIntegrationRepository{}
}

func (r *customerProviderIntegrationRepository) FindByCustomerAndProvider(customerID uint, provider string) (*models.CustomerProviderIntegration, error) {
	return first[models.CustomerProviderIntegration](facades.Orm().Query().
		Where("customer_id", customerID).
		Where("provider", provider), "customer provider integration repo: find by customer+provider")
}

func (r *customerProviderIntegrationRepository) FindByCustomerIDsAndProvider(customerIDs []uint, provider string) ([]models.CustomerProviderIntegration, error) {
	if len(customerIDs) == 0 {
		return nil, nil
	}
	var integrations []models.CustomerProviderIntegration
	if err := facades.Orm().Query().
		Where("provider", provider).
		Where("customer_id IN ?", customerIDs).
		Find(&integrations); err != nil {
		return nil, fmt.Errorf("customer provider integration repo: find by customer ids+provider: %w", err)
	}
	return integrations, nil
}

func (r *customerProviderIntegrationRepository) FindByExternalCustomerID(provider, externalCustomerID string) (*models.CustomerProviderIntegration, error) {
	return first[models.CustomerProviderIntegration](facades.Orm().Query().Where("(? = '' OR customer_id IN (SELECT id FROM customers WHERE environment=?))", r.environment, r.environment).
		Where("provider", provider).
		Where("external_customer_id", externalCustomerID), "customer provider integration repo: find by external customer id")
}

func (r *customerProviderIntegrationRepository) ListByCustomer(customerID uint) ([]models.CustomerProviderIntegration, error) {
	var integrations []models.CustomerProviderIntegration
	if err := facades.Orm().Query().
		Where("customer_id", customerID).
		Order("provider asc").
		Find(&integrations); err != nil {
		return nil, fmt.Errorf("customer provider integration repo: list by customer: %w", err)
	}
	return integrations, nil
}

func (r *customerProviderIntegrationRepository) Create(integration *models.CustomerProviderIntegration) error {
	if err := facades.Orm().Query().Create(integration); err != nil {
		return fmt.Errorf("customer provider integration repo: create: %w", taken(err))
	}
	return nil
}

func (r *customerProviderIntegrationRepository) Update(integration *models.CustomerProviderIntegration) error {
	written, err := updateLiveLink(facades.Orm().Query(), integration)
	if err != nil {
		return fmt.Errorf("customer provider integration repo: update: %w", taken(err))
	}
	if !written {
		return fmt.Errorf("customer provider integration repo: update: %w", ErrIntegrationGone)
	}
	return nil
}

// updateLiveLink writes every column of link to its row while it is live.
func updateLiveLink(q orm.Query, link *models.CustomerProviderIntegration) (bool, error) {
	if link.Metadata == "" {
		link.Metadata = "{}"
	}
	return updateLive(q, link)
}

func (r *customerProviderIntegrationRepository) Unlink(
	customerID uint, provider string, kyc *models.CustomerProviderKyc,
	record func(link *models.CustomerProviderIntegration) *models.AdminAuditLog,
) error {
	err := facades.Orm().Transaction(func(tx orm.Query) error {
		link, err := first[models.CustomerProviderIntegration](tx.LockForUpdate().
			Where("customer_id", customerID).Where("provider", provider), "lock link")
		if err != nil {
			return err
		}
		if link == nil {
			return ErrIntegrationGone
		}
		if err := refuseMoneyInFlight(tx, customerID, provider); err != nil {
			return err
		}
		res, err := tx.Delete(link)
		if err != nil {
			return err
		}
		if res.RowsAffected == 0 {
			return ErrIntegrationGone
		}
		if err := forgetKyc(tx, kyc); err != nil {
			return err
		}
		if err := deleteOfframpDestinations(tx, customerID, provider); err != nil {
			return err
		}
		return createAuditLog(tx, record(link))
	})
	if err != nil {
		return fmt.Errorf("customer provider integration repo: unlink: %w", err)
	}
	return nil
}

// errDetachRowMoved rolls a detach back when the KYC row moved off the status
// it was decided from. It never leaves DetachCustomer.
var errDetachRowMoved = errors.New("kyc row moved")

func (r *customerProviderIntegrationRepository) DetachCustomer(
	link *models.CustomerProviderIntegration, reset, read *models.CustomerProviderKyc,
) (bool, error) {
	var detached *models.CustomerProviderIntegration
	err := facades.Orm().Transaction(func(tx orm.Query) error {
		locked, err := first[models.CustomerProviderIntegration](tx.LockForUpdate().Where("id", link.ID), "lock link")
		if err != nil {
			return err
		}
		if locked == nil {
			return ErrIntegrationGone
		}
		if customerOf(locked) != customerOf(link) {
			return ErrLinkConflict
		}
		active, err := tx.Model(&models.ProviderKycSubmissionIntent{}).
			Where("customer_id", locked.CustomerID).Where("provider", locked.Provider).
			Where("state IN ?", models.ActiveKycIntentStates).Exists()
		if err != nil {
			return err
		}
		if active {
			return ErrActiveKycIntentExists
		}
		if err := refuseMoneyInFlight(tx, locked.CustomerID, locked.Provider); err != nil {
			return err
		}
		if err := recordDetachedCustomer(tx, locked); err != nil {
			return err
		}
		locked.ExternalCustomerID = nil
		locked.Status = models.IntegrationStatusPending
		if _, err := updateLiveLink(tx, locked); err != nil {
			return err
		}
		if err := deleteOfframpDestinations(tx, locked.CustomerID, locked.Provider); err != nil {
			return err
		}
		categories := read.RejectionCategories
		if categories == "" {
			categories = "[]"
		}
		written, err := updateLiveKyc(tx.Where("kyc_status", read.KycStatus).
			Where("rejection_categories = ?::jsonb", categories), reset)
		if err != nil {
			return err
		}
		if !written {
			return errDetachRowMoved
		}
		detached = locked
		return nil
	})
	if errors.Is(err, errDetachRowMoved) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("customer provider integration repo: detach customer: %w", err)
	}
	*link = *detached
	return true, nil
}

// recordDetachedCustomer records the provider-side customer link names as
// detached, inside the detach's transaction. A link with no customer has
// nothing to record, and a customer recorded already stays as first recorded.
func recordDetachedCustomer(tx orm.Query, link *models.CustomerProviderIntegration) error {
	customer := customerOf(link)
	if customer == "" {
		return nil
	}
	_, err := tx.Exec(`INSERT INTO provider_detached_customers
		(customer_id, provider, external_customer_id, detached_at, created_at, updated_at)
		VALUES (?, ?, ?, NOW(), NOW(), NOW())
		ON CONFLICT (provider, external_customer_id) DO NOTHING`,
		link.CustomerID, link.Provider, customer)
	return err
}

func (r *customerProviderIntegrationRepository) IsDetachedCustomer(provider, externalCustomerID string) (bool, error) {
	found, err := facades.Orm().Query().Model(&models.ProviderDetachedCustomer{}).
		Where("provider", provider).Where("external_customer_id", externalCustomerID).Exists()
	if err != nil {
		return false, fmt.Errorf("customer provider integration repo: is detached customer: %w", err)
	}
	return found, nil
}

// customerOf is the provider-side customer id a link names, "" for none.
func customerOf(link *models.CustomerProviderIntegration) string {
	if link.ExternalCustomerID == nil {
		return ""
	}
	return *link.ExternalCustomerID
}

// refuseMoneyInFlight answers why money can still move at provider for the
// customer — a standing account active or being provisioned, a transaction
// not yet terminal — read inside the unlink's own transaction.
func refuseMoneyInFlight(tx orm.Query, customerID uint, provider string) error {
	return refuseOpen(
		func() (bool, error) {
			return (&customerVirtualAccountRepository{}).HasOpenByCustomerAndProviderTx(tx, customerID, provider)
		},
		func() (bool, error) {
			return (&transactionRepository{}).HasOpenByCustomerAndProviderTx(tx, customerID, provider)
		},
	)
}

// refuseMoneyInFlightAnywhere is refuseMoneyInFlight at every provider, for
// the deletion of a whole account: what its individual customer, whose id is
// the user's, still has in flight (spec §5 F2). A company's money in flight
// is the company's, which the deletion of one member does not settle (§18.5).
func refuseMoneyInFlightAnywhere(tx orm.Query, userID uint) error {
	return refuseOpen(
		func() (bool, error) { return (&customerVirtualAccountRepository{}).HasOpenByCustomerTx(tx, userID) },
		func() (bool, error) { return (&transactionRepository{}).HasOpenByCustomerTx(tx, userID) },
	)
}

// refuseOpen is the one decision both guards share: a standing account that
// can still receive money outranks an order still on its way, and each answers
// with its own sentinel.
func refuseOpen(accountOpen, transactionOpen func() (bool, error)) error {
	open, err := accountOpen()
	if err != nil {
		return err
	}
	if open {
		return ErrVirtualAccountOpen
	}
	open, err = transactionOpen()
	if err != nil {
		return err
	}
	if open {
		return ErrTransactionOpen
	}
	return nil
}

// forgetKyc writes the cleared row and soft-deletes it; nil is no row.
func forgetKyc(tx orm.Query, kyc *models.CustomerProviderKyc) error {
	if kyc == nil {
		return nil
	}
	if _, err := updateLiveKyc(tx, kyc); err != nil {
		return err
	}
	_, err := tx.Delete(kyc)
	return err
}

func (r *customerProviderIntegrationRepository) AdminPaginate(filters AdminKycListFilters) ([]AdminKycRow, int64, error) {
	where, args := buildAdminKycWhere(filters)
	column, dir := adminKycSort.parse(filters.Sort)
	order := column + " " + dir + " NULLS LAST, i.id DESC, c.id DESC"

	countSQL := "SELECT COUNT(1) " + adminKycBaseSQL + where
	var total int64
	if err := facades.Orm().Query().Raw(countSQL, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("customer provider integration repo: admin count: %w", err)
	}
	if total == 0 {
		return []AdminKycRow{}, 0, nil
	}

	// requests.Paging is the one clamp for every listing; this only keeps a
	// page below 1 from becoming a negative OFFSET, which Postgres refuses
	// where the ORM's Paginate would have dropped it.
	offset := max(filters.Page-1, 0) * filters.PerPage
	keySQL := "SELECT i.id AS integration_id, c.id AS customer_id " + adminKycBaseSQL + where +
		" ORDER BY " + order + " LIMIT ? OFFSET ?"
	keyArgs := append(append([]any{}, args...), filters.PerPage, offset)
	var keys []adminKycKey
	if err := facades.Orm().Query().Raw(keySQL, keyArgs...).Scan(&keys); err != nil {
		return nil, 0, fmt.Errorf("customer provider integration repo: admin list ids: %w", err)
	}
	if len(keys) == 0 {
		return []AdminKycRow{}, total, nil
	}

	rows, err := r.loadAdminPage(keys)
	if err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// loadAdminPage loads the rows keys name, in their order: a link's with its
// holder and KYC row (loadAdminRows), a holder's with no link with the holder
// alone (loadUnlinkedHolders).
func (r *customerProviderIntegrationRepository) loadAdminPage(keys []adminKycKey) ([]AdminKycRow, error) {
	var linkIDs, holderIDs []uint
	for _, key := range keys {
		if key.IntegrationID != nil {
			linkIDs = append(linkIDs, *key.IntegrationID)
		} else {
			holderIDs = append(holderIDs, key.CustomerID)
		}
	}
	links, err := r.loadAdminRows(linkIDs)
	if err != nil {
		return nil, err
	}
	byLink := make(map[uint]AdminKycRow, len(links))
	for _, row := range links {
		byLink[row.Integration.ID] = row
	}
	byHolder, err := r.loadUnlinkedHolders(holderIDs)
	if err != nil {
		return nil, err
	}
	rows := make([]AdminKycRow, 0, len(keys))
	for _, key := range keys {
		var row AdminKycRow
		var ok bool
		if key.IntegrationID != nil {
			row, ok = byLink[*key.IntegrationID]
		} else {
			row, ok = byHolder[key.CustomerID]
		}
		if ok {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

// loadUnlinkedHolders is the row of each of ids, holders no provider row
// names: the customer and, for a person, its account.
func (r *customerProviderIntegrationRepository) loadUnlinkedHolders(ids []uint) (map[uint]AdminKycRow, error) {
	if len(ids) == 0 {
		return map[uint]AdminKycRow{}, nil
	}
	var customers []models.Customer
	if err := facades.Orm().Query().Where("id IN ?", ids).Find(&customers); err != nil {
		return nil, fmt.Errorf("customer provider integration repo: admin load holders: %w", err)
	}
	var users []models.User
	if err := facades.Orm().Query().Where("id IN ?", ids).Find(&users); err != nil {
		return nil, fmt.Errorf("customer provider integration repo: admin load holders' accounts: %w", err)
	}
	accounts := make(map[uint]*models.User, len(users))
	for i := range users {
		accounts[users[i].ID] = &users[i]
	}
	out := make(map[uint]AdminKycRow, len(customers))
	for i := range customers {
		row := AdminKycRow{Customer: &customers[i]}
		if customers[i].Type == models.CustomerTypeIndividual {
			row.User = accounts[customers[i].ID]
		}
		out[customers[i].ID] = row
	}
	return out, nil
}

// loadAdminRows loads the explorer rows of the given integration ids, in
// their order, each with its holder, its user and live KYC row.
func (r *customerProviderIntegrationRepository) loadAdminRows(ids []uint) ([]AdminKycRow, error) {
	if len(ids) == 0 {
		return []AdminKycRow{}, nil
	}
	var integrations []models.CustomerProviderIntegration
	if err := facades.Orm().Query().With("User").With("Customer").Where("id IN ?", ids).Find(&integrations); err != nil {
		return nil, fmt.Errorf("customer provider integration repo: admin load integrations: %w", err)
	}

	byID := make(map[uint]models.CustomerProviderIntegration, len(integrations))
	pairs := make([]kycPair, 0, len(integrations))
	for i := range integrations {
		byID[integrations[i].ID] = integrations[i]
		pairs = append(pairs, kycPair{
			customerID: integrations[i].CustomerID, provider: integrations[i].Provider,
		})
	}

	kycMap, err := r.loadKycByPairs(pairs)
	if err != nil {
		return nil, err
	}

	rows := make([]AdminKycRow, 0, len(ids))
	for _, id := range ids {
		integration, ok := byID[id]
		if !ok {
			continue
		}
		key := kycPairKey(integration.CustomerID, integration.Provider)
		rows = append(rows, adminKycRow(&integration, kycMap[key]))
	}
	return rows, nil
}

// adminKycRow is the explorer row of a link loaded with its holder and user.
func adminKycRow(integration *models.CustomerProviderIntegration, kyc *models.CustomerProviderKyc) AdminKycRow {
	return AdminKycRow{Integration: integration, Kyc: kyc, Customer: integration.Customer, User: integration.User}
}

func (r *customerProviderIntegrationRepository) AdminUnclassifiedRejections() ([]AdminKycRow, error) {
	var ids []uint
	if err := facades.Orm().Query().Raw("SELECT i.id "+adminKycBaseSQL+
		" AND k.kyc_status = ? AND k.rejection_categories = '[]'::jsonb ORDER BY i.id",
		models.KycStatusRejected).Scan(&ids); err != nil {
		return nil, fmt.Errorf("customer provider integration repo: admin unclassified rejections: %w", err)
	}
	if len(ids) == 0 {
		return []AdminKycRow{}, nil
	}
	return r.loadAdminRows(ids)
}

func (r *customerProviderIntegrationRepository) FindByIDForAdmin(id uint) (*AdminKycRow, error) {
	integration, err := first[models.CustomerProviderIntegration](facades.Orm().Query().With("User").With("Customer").Where("id", id),
		"customer provider integration repo: find by id")
	if err != nil || integration == nil {
		return nil, err
	}
	kyc, err := r.findKyc(integration.CustomerID, integration.Provider)
	if err != nil {
		return nil, err
	}
	row := adminKycRow(integration, kyc)
	return &row, nil
}

func (r *customerProviderIntegrationRepository) findKyc(customerID uint, provider string) (*models.CustomerProviderKyc, error) {
	return first[models.CustomerProviderKyc](facades.Orm().Query().
		Where("customer_id", customerID).
		Where("provider", provider), "customer provider integration repo: find kyc")
}

func (r *customerProviderIntegrationRepository) loadKycByPairs(pairs []kycPair) (map[string]*models.CustomerProviderKyc, error) {
	if len(pairs) == 0 {
		return map[string]*models.CustomerProviderKyc{}, nil
	}

	clauses := make([]string, 0, len(pairs))
	args := make([]any, 0, len(pairs)*2)
	seen := make(map[string]struct{}, len(pairs))
	for _, p := range pairs {
		key := kycPairKey(p.customerID, p.provider)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		clauses = append(clauses, "(customer_id = ? AND provider = ?)")
		args = append(args, p.customerID, p.provider)
	}

	var kycs []models.CustomerProviderKyc
	sql := "SELECT * FROM customer_provider_kycs WHERE deleted_at IS NULL AND (" +
		strings.Join(clauses, " OR ") + ")"
	if err := facades.Orm().Query().Raw(sql, args...).Scan(&kycs); err != nil {
		return nil, fmt.Errorf("customer provider integration repo: batch load kycs: %w", err)
	}

	out := make(map[string]*models.CustomerProviderKyc, len(kycs))
	for i := range kycs {
		key := kycPairKey(kycs[i].CustomerID, kycs[i].Provider)
		out[key] = &kycs[i]
	}
	return out, nil
}

func kycPairKey(customerID uint, provider string) string {
	return fmt.Sprintf("%d:%s", customerID, provider)
}

func buildAdminKycWhere(filters AdminKycListFilters) (string, []any) {
	clauses := make([]string, 0, 12)
	args := make([]any, 0, 12)

	// A holder with no link is a row only when asked for, and then the only
	// kind: it has no status at any provider.
	if filters.KycStatus == AdminKycNoProvider {
		clauses = append(clauses, "i.id IS NULL")
	} else {
		clauses = append(clauses, "i.id IS NOT NULL")
	}
	if filters.CustomerType != "" {
		clauses = append(clauses, "c.type = ?")
		args = append(args, filters.CustomerType)
	}
	if filters.CustomerStatus != "" {
		clauses = append(clauses, "c.status = ?")
		args = append(args, filters.CustomerStatus)
	}

	if filters.Provider != "" {
		clauses = append(clauses, "i.provider = ?")
		args = append(args, filters.Provider)
	}
	if filters.IntegrationStatus != "" {
		clauses = append(clauses, "i.status = ?")
		args = append(args, filters.IntegrationStatus)
	}
	if filters.KycStatus != "" && filters.KycStatus != AdminKycNoProvider {
		if filters.KycStatus == models.KycStatusNotStarted {
			clauses = append(clauses, "(k.id IS NULL OR k.kyc_status = ?)")
			args = append(args, models.KycStatusNotStarted)
		} else {
			clauses = append(clauses, "k.kyc_status = ?")
			args = append(args, filters.KycStatus)
		}
	}
	if filters.TosStatus != "" {
		clauses = append(clauses, "k.tos_status = ?")
		args = append(args, filters.TosStatus)
	}
	if filters.CollectionMode != "" {
		clauses = append(clauses, "k.collection_mode = ?")
		args = append(args, filters.CollectionMode)
	}
	if filters.Search != "" {
		like := likeContains(filters.Search)
		// A company's rows are found by its members' handles and e-mails.
		clauses = append(clauses, `(u.handle ILIKE ? OR u.email ILIKE ? OR COALESCE(i.external_customer_id, '') ILIKE ?
			OR (c.type = 'business' AND EXISTS (SELECT 1 FROM customer_members m JOIN users mu ON mu.id = m.user_id
				WHERE m.customer_id = c.id AND (mu.handle ILIKE ? OR mu.email ILIKE ?))))`)
		args = append(args, like, like, like, like, like)
	}
	if filters.SyncedBefore != nil {
		clauses = append(clauses, "k.last_synced_at < ?")
		args = append(args, *filters.SyncedBefore)
	}
	if filters.Rail != "" {
		clause, railArgs := adminKycRailClause(filters.Rail)
		clauses = append(clauses, clause)
		args = append(args, railArgs...)
	}
	refusals, refusalArgs := adminKycRefusalClauses(filters)
	clauses = append(clauses, refusals...)
	args = append(args, refusalArgs...)

	return " AND " + strings.Join(clauses, " AND "), args
}

// adminKycRailClause matches rows whose endorsements clear the requested rail.
// Filtering in Go would break pagination; the jsonb probe mirrors SupportsRail:
// the directional endorsement decides wherever the row carries one, and the
// umbrella only a row with none.
func adminKycRailClause(rail string) (string, []any) {
	names, ok := models.RailEndorsementFor(rail)
	if !ok {
		return "FALSE", nil
	}
	const endorsed = "EXISTS (SELECT 1 FROM jsonb_array_elements(k.endorsements) elem WHERE elem->>'name' = ? AND elem->>'status' = ?)"
	const carries = "EXISTS (SELECT 1 FROM jsonb_array_elements(k.endorsements) elem WHERE elem->>'name' = ?)"
	clause := "k.kyc_status = ? AND (" + endorsed + " OR (NOT " + carries + " AND " + endorsed + "))"
	return clause, []any{
		models.KycStatusApproved,
		names.Directional, models.EndorsementStatusApproved,
		names.Directional,
		names.Umbrella, models.EndorsementStatusApproved,
	}
}

// adminKycRefusalClauses matches why a row was refused: a category it
// carries, whether the refusal is final, and a submission that never took.
// A link with no compliance row carries no category, is not terminal and
// holds no error — the COALESCE keeps it on the negated side.
func adminKycRefusalClauses(filters AdminKycListFilters) ([]string, []any) {
	var clauses []string
	var args []any
	if filters.RejectionCategory != "" {
		clauses = append(clauses, "k.rejection_categories @> jsonb_build_array(?::text)")
		args = append(args, filters.RejectionCategory)
	}
	if filters.Terminal != nil {
		clause, terminalArgs := adminKycTerminalClause(filters.TerminalKycIDs)
		if !*filters.Terminal {
			clause = "NOT COALESCE(" + clause + ", FALSE)"
		}
		clauses = append(clauses, clause)
		args = append(args, terminalArgs...)
	}
	if filters.HasError != nil {
		clause := "k.last_error_code IS NOT NULL"
		if !*filters.HasError {
			clause = "k.last_error_code IS NULL"
		}
		clauses = append(clauses, clause)
	}
	return clauses, args
}

// adminKycTerminalClause is kycsvc.Terminal as a query: rejected with any
// category KycRejectionIsTerminal names — the categories come from the
// model's vocabulary, so a new terminal category reaches the filter from the
// same edit that makes it terminal — or one of the unclassified rejections
// the KYC service found terminal (unclassified).
func adminKycTerminalClause(unclassified []uint) (string, []any) {
	placeholders := []string{}
	args := []any{models.KycStatusRejected}
	for _, category := range models.KycRejectionCategories {
		if !models.KycRejectionIsTerminal(category) {
			continue
		}
		placeholders = append(placeholders, "?")
		args = append(args, category)
	}
	clause := fmt.Sprintf(
		"(k.kyc_status = ? AND EXISTS (SELECT 1 FROM jsonb_array_elements_text(k.rejection_categories) category WHERE category IN (%s)))",
		strings.Join(placeholders, ","),
	)
	if len(unclassified) > 0 {
		clause = "(" + clause + " OR k.id IN ?)"
		args = append(args, unclassified)
	}
	return clause, args
}

var adminKycSort = sortable{
	columns: map[string]string{"last_synced_at": "k.last_synced_at", "updated_at": "i.updated_at", "created_at": "i.created_at"},
	column:  "i.updated_at",
	dir:     "desc",
}

// NewCustomerProviderIntegrationRepositoryForEnvironment bounds resolved webhook owners.
func NewCustomerProviderIntegrationRepositoryForEnvironment(mode string) CustomerProviderIntegrationRepository {
	return &customerProviderIntegrationRepository{environment: mode}
}
