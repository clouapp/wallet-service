package repositories

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// ErrDuplicateDeposit is Create losing uniq_tx_provider_deposit: another
// writer (the webhook, for a drain) recorded the provider's deposit first.
var ErrDuplicateDeposit = errors.New("transaction repo: deposit already recorded")

// TransactionRepository is the persistence boundary for Transaction records.
type TransactionRepository interface {
	Create(tx *models.Transaction) error
	Update(tx *models.Transaction) error
	// TouchUpdatedAt moves updated_at to now and writes nothing else, so a row
	// the sweep cannot act on goes to the back of the oldest-first order.
	TouchUpdatedAt(id uint) error
	// MarkAttention sets the attention mark when it is not already that code. It
	// reports whether the row changed, so a poll does not rewrite it.
	MarkAttention(txID uint, code string, now time.Time) (bool, error)
	// ClearAttention drops the mark, reporting whether there was one.
	ClearAttention(txID uint) (bool, error)
	// RecordSupportTicket writes the ticket and the audit row in one transaction.
	// It reports false, writing nothing, when the transaction is not there.
	RecordSupportTicket(txID uint, ref, note string, now time.Time, entry *models.AdminAuditLog) (bool, error)
	// ListByStatus returns up to limit transactions in status, oldest
	// updated_at first.
	ListByStatus(status string, limit int) ([]models.Transaction, error)
	// ListReturningBefore returns up to limit transactions still returning
	// created before cutoff, oldest first. The age runs from creation, not
	// updated_at: every webhook and refresh resets that, which could hide a
	// held payout for good.
	ListReturningBefore(cutoff time.Time, limit int) ([]models.Transaction, error)
	// ListWithAttention returns up to limit transactions carrying an attention
	// mark, the first marked first.
	ListWithAttention(limit int) ([]models.Transaction, error)
	// SaveProviderSnapshotIfStatus atomically persists status plus every
	// settlement column when the row still holds previousStatus at write time.
	// Use only after applying a fresh provider response (applyOrder/ApplyDeposit)
	// onto the in-memory row — it is the single write for a provider round-trip,
	// and the caller never has to branch on whether the status itself moved: a
	// settled amount or exchange rate can change while the order is still
	// `processing`, and this writes that too, in the same CAS.
	SaveProviderSnapshotIfStatus(tx *models.Transaction, previousStatus string) (bool, error)
	// DiscardReservation removes a reservation row whose create the provider
	// refused before building a request, and frees the quote it was holding.
	// It exists because that refusal is not a failed order: recording it as one
	// is what made half of production's on-ramp rows read as "provider
	// unreachable" against a provider that answered instantly and correctly.
	DiscardReservation(tx *models.Transaction) error
	// FindByProviderAndDepositID is the row a provider's deposit or drain id
	// opened, without a lock: what a create that lost uniq_tx_provider_deposit
	// (ErrDuplicateDeposit) reads back. A miss is (nil, nil).
	FindByProviderAndDepositID(provider, depositID string) (*models.Transaction, error)
	// FindByProviderAndDepositIDForUpdate is the lookup for the virtual-account
	// on-ramp, where the provider's deposit id is the order identity: there is
	// no client create, so neither the idempotency key nor the estimate applies.
	// A miss is (nil, nil) and is the expected result of the first event for a
	// deposit — that miss is what creates the transaction.
	//
	// FindByCustomerAndEstimate is the ramp create-dedup lookup. The estimate id
	// is what a ramp order is idempotent on — the database enforces
	// `(customer_id, provider, estimate_id)` — and txType keeps a quote consumed
	// by one direction from surfacing the other's row.
	FindByCustomerAndEstimate(customerID uint, provider string, estimateID uint, txType string) (*models.Transaction, error)
	FindByCustomerAndExternalID(customerID uint, externalID string) (*models.Transaction, error)
	// FindByExternalID reads a transaction by its external id whoever holds
	// it, for an operator acting on what the attention queue showed. A miss,
	// an id that cannot be a uuid included, is (nil, nil).
	FindByExternalID(externalID string) (*models.Transaction, error)
	// FindLatestByCustomer returns the most recent on-ramp or off-ramp row of
	// the customer by created_at, or (nil, nil) when it has none.
	FindLatestByCustomer(customerID uint) (*models.Transaction, error)
	// FindLatestOfframpDepositAddress is the deposit address the provider gave
	// this customer's newest off-ramp of token, or "" before the first one.
	// Bridge gives a customer of its own one address, so it is where the next
	// deposit goes, and whether that address already holds the token decides
	// what the deposit costs in SOL. It is the customer's, not the member's: a
	// person and their company are two customers there, with two addresses.
	FindLatestOfframpDepositAddress(customerID uint, provider, token string) (string, error)
	// HasOpenByCustomerAndProvider reports whether the customer has a
	// transaction at provider that has not reached a terminal status — money in
	// flight there.
	HasOpenByCustomerAndProvider(customerID uint, provider string) (bool, error)
	Paginate(filters TransactionListFilters) ([]models.Transaction, int64, error)
	// AdminPaginate is the admin-facing, cross-user listing. Empty/zero/nil
	// filters are ignored. Eager-loads the owning User.
	AdminPaginate(filters AdminTransactionListFilters) ([]models.Transaction, int64, error)
	// FindByID loads a single transaction by internal ID with its owning User.
	FindByID(id uint) (*models.Transaction, error)
	// FindStaleRampOrders returns non-terminal ramp orders last touched before
	// updatedBefore, oldest first. It is what the reconciliation sweep reads:
	// orders the event stream should have resolved by now and did not. Orders
	// whose funding authorization is finalizing, unknown or needs_operator are
	// left out: recovery owns them.
	FindStaleRampOrders(updatedBefore time.Time, limit int) ([]models.Transaction, error)
	// FindUndeliveredOnramps returns on-ramps whose pay-in settled but crypto is
	// still in the provider's custody (no delivery id, no on-chain signature).
	//
	// It is scoped to providers because most have nothing to deliver — an
	// account provisioned against the user's own address settles in one hop —
	// and an unscoped query would rescan every on-ramp those providers ever
	// completed, forever, to do nothing with them. The caller passes the codes
	// whose adapter actually implements the second hop.
	//
	// An empty provider list returns no rows rather than all of them: "nobody
	// delivers in two hops" is a real state, and reading it as "no filter"
	// would turn the safety net into a full-table scan.
	//
	// An order that already spent maxAttempts deliveries is not returned: it is
	// an operator's from there.
	FindUndeliveredOnramps(providers []string, maxAttempts, limit int) ([]models.Transaction, error)
	// FindDeliveriesInFlight returns on-ramps of these providers whose delivery
	// was placed and has not been seen to land or fail, last touched before
	// updatedBefore, oldest first. It is how the sweep notices a delivery whose
	// own webhook was lost.
	FindDeliveriesInFlight(providers []string, updatedBefore time.Time, limit int) ([]models.Transaction, error)
	// SaveDeliveryOutcome writes the delivery columns of tx — status, signature,
	// completion, failure, delivery id and attempt count — only while the row
	// still names previousDeliveryOrderID and carries no signature. It reports
	// whether it wrote, so two readers of the same outcome count it once.
	SaveDeliveryOutcome(tx *models.Transaction, previousDeliveryOrderID string) (bool, error)
	// FindOpenOfframpsAwaitingSettlement lists off-ramps waiting for payout
	// after funding (no provider transfer id yet), oldest first.
	FindOpenOfframpsAwaitingSettlement(customerID uint, provider string) ([]models.Transaction, error)
	// ClaimOfframpDeposit records txHash on the oldest of the user's open
	// off-ramps that match accepts and that no deposit has claimed, with the
	// candidates locked FOR UPDATE so two deposits cannot claim one order. An
	// off-ramp already carrying txHash — open or settled since — is returned
	// unchanged: a redelivered event claims nothing new. It returns nil when no
	// order matches.
	ClaimOfframpDeposit(customerID uint, provider, txHash string, match func(*models.Transaction) bool) (*models.Transaction, error)
	// FindOnrampsInProviderCustody lists the user's on-ramps whose pay-in
	// settled and whose tokens have not been delivered yet: what still sits in
	// the provider's managed wallet on the user's behalf.
	FindOnrampsInProviderCustody(customerID uint, provider string) ([]models.Transaction, error)
	// ExpireUnfunded marks tx expired only while the row is still pending or
	// awaiting funds and, for an Ozav off-ramp, no deposit has claimed it. The
	// caller's copy may predate a concurrent claim, so the guard is the row's,
	// not the struct's. In the same database transaction a prepared funding
	// authorization is failed with quote_expired; an order whose authorization
	// has left prepared (its funding may be under way) is never expired. It
	// reports whether it wrote, and sets tx.Status if so.
	ExpireUnfunded(tx *models.Transaction) (bool, error)
	// CancelUnfundedOfframp fails an off-ramp with reason only while it is
	// still awaiting funds and no deposit has claimed it. It reports whether it
	// wrote, and sets tx's status and reason if so.
	CancelUnfundedOfframp(tx *models.Transaction, reason string) (bool, error)
}

// TransactionListFilters scopes a paginated transaction listing to one
// customer's orders.
type TransactionListFilters struct {
	CustomerID uint
	Type       string
	Status     string
	RampOnly   bool       // when true and Type is empty, restrict to on/off-ramp rows
	From       *time.Time // inclusive lower bound on created_at
	To         *time.Time // exclusive upper bound on created_at
	Page       int
	PerPage    int
}

// AdminTransactionListFilters scopes the admin-facing transaction listing.
// Empty string / zero / nil fields are ignored by AdminPaginate.
type AdminTransactionListFilters struct {
	Provider string
	Type     string
	Status   string
	Rail     string
	UserID   uint
	// CustomerID is whose money the orders moved; UserID who placed them.
	CustomerID uint
	Search     string     // matched against external_id and gateway_order_id
	From       *time.Time // inclusive lower bound on created_at
	To         *time.Time // exclusive upper bound on created_at
	Page       int
	PerPage    int
	Sort       string // "created_at" | "-created_at"
}

type transactionRepository struct {
	tenant, environment string
	customerID          uint
}

// NewTransactionRepository returns a TransactionRepository backed by the default ORM connection.
func NewTransactionRepository() TransactionRepository {
	return &transactionRepository{}
}

// Create writes a client's order reservation, and only while the member placing
// it is still a live account (lockLiveUser): an order placed by a request that
// was authenticated before the account's deletion committed is refused with
// ErrUserDeleted. Likewise for the customer (lockOpenCustomer): one whose close
// committed first refuses it with ErrCustomerClosed.
// A row that carries a deposit id another row already holds (the webhook
// recorded the same drain first) answers ErrDuplicateDeposit.
// CreateTx stays the plain insert, for the webhook's transaction, which
// records what a provider reports whoever holds the account now.
func (r *transactionRepository) Create(tx *models.Transaction) error {
	err := facades.Orm().Transaction(func(q orm.Query) error {
		if err := lockLiveUser(q, tx.UserID); err != nil {
			return fmt.Errorf("transaction repo: create: %w", err)
		}
		if err := lockOpenCustomer(q, tx.CustomerID); err != nil {
			return fmt.Errorf("transaction repo: create: %w", err)
		}
		return r.CreateTx(q, tx)
	})
	if err != nil && tx.DepositID != nil && isUniqueViolation(err) {
		return fmt.Errorf("%w: %w", ErrDuplicateDeposit, err)
	}
	return err
}

func (r *transactionRepository) FindByProviderAndDepositID(provider, depositID string) (*models.Transaction, error) {
	return first[models.Transaction](facades.Orm().Query().
		Where("provider", provider).
		Where("deposit_id", depositID), "transaction repo: find by provider+deposit id")
}

func (r *transactionRepository) CreateTx(q orm.Query, tx *models.Transaction) error {
	if err := q.Create(tx); err != nil {
		return fmt.Errorf("transaction repo: create: %w", err)
	}
	return nil
}

func (r *transactionRepository) Update(tx *models.Transaction) error {
	return r.UpdateTx(facades.Orm().Query(), tx)
}

func (r *transactionRepository) TouchUpdatedAt(id uint) error {
	if _, err := facades.Orm().Query().Model(&models.Transaction{}).Where("id", id).
		Update("updated_at", time.Now()); err != nil {
		return fmt.Errorf("transaction repo: touch updated_at: %w", err)
	}
	return nil
}

func (r *transactionRepository) MarkAttention(txID uint, code string, now time.Time) (bool, error) {
	result, err := facades.Orm().Query().Model(&models.Transaction{}).
		Where("id", txID).
		Where("(attention_code IS NULL OR attention_code <> ?)", code).
		Update(map[string]any{"attention_code": code, "attention_at": now})
	if err != nil {
		return false, fmt.Errorf("transaction repo: mark attention: %w", err)
	}
	return result.RowsAffected > 0, nil
}

func (r *transactionRepository) ClearAttention(txID uint) (bool, error) {
	result, err := facades.Orm().Query().Model(&models.Transaction{}).
		Where("id", txID).
		Where("attention_code IS NOT NULL").
		Update(map[string]any{"attention_code": nil, "attention_at": nil})
	if err != nil {
		return false, fmt.Errorf("transaction repo: clear attention: %w", err)
	}
	return result.RowsAffected > 0, nil
}

func (r *transactionRepository) RecordSupportTicket(txID uint, ref, note string, now time.Time, entry *models.AdminAuditLog) (bool, error) {
	recorded := false
	err := facades.Orm().Transaction(func(q orm.Query) error {
		result, err := q.Model(&models.Transaction{}).Where("id", txID).Update(map[string]any{
			"support_ticket_ref":  ref,
			"support_ticket_note": note,
			"support_ticket_at":   now,
		})
		if err != nil {
			return fmt.Errorf("record ticket: %w", err)
		}
		if result.RowsAffected == 0 {
			return nil
		}
		if err := createAuditLog(q, entry); err != nil {
			return fmt.Errorf("record ticket audit: %w", err)
		}
		recorded = true
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("transaction repo: support ticket: %w", err)
	}
	return recorded, nil
}

func (r *transactionRepository) DiscardReservation(tx *models.Transaction) error {
	// The estimate link has to be dropped in the database, not only in memory.
	// The unique on (customer_id, provider, estimate_id) counts soft-deleted rows,
	// so a discarded row that kept the link would hold the quote forever and
	// the retry the refusal invites would collide on the insert.
	if _, err := facades.Orm().Query().Model(tx).Update(map[string]any{
		"estimate_id": nil,
		"expires_at":  nil,
	}); err != nil {
		return fmt.Errorf("transaction repo: discard reservation (unlink): %w", err)
	}
	tx.EstimateID = nil
	tx.ExpiresAt = nil
	if _, err := facades.Orm().Query().Delete(tx); err != nil {
		return fmt.Errorf("transaction repo: discard reservation: %w", err)
	}
	return nil
}

func (r *transactionRepository) SaveProviderSnapshotIfStatus(tx *models.Transaction, previousStatus string) (bool, error) {
	updates := settlementUpdates(tx)
	updates["status"] = tx.Status
	result, err := facades.Orm().Query().
		Model(tx).
		Where("status", previousStatus).
		Update(updates)
	if err != nil {
		return false, fmt.Errorf("transaction repo: save provider snapshot if status: %w", err)
	}
	return result.RowsAffected > 0, nil
}

func settlementUpdates(tx *models.Transaction) map[string]any {
	updates := map[string]any{
		"amount_fiat":  tx.AmountFiat,
		"amount_token": tx.AmountToken,
		"raw_payload":  tx.RawPayload,
	}
	putIfSet(updates, "fee_amount", tx.FeeAmount)
	putIfSet(updates, "fee_currency", tx.FeeCurrency)
	putIfSet(updates, "expires_at", tx.ExpiresAt)
	putIfSet(updates, "deposit_instructions", tx.DepositInstructions)
	putIfSet(updates, "exchange_rate", tx.ExchangeRate)
	putIfSet(updates, "deposit_tx_signature", tx.DepositTxSignature)
	putIfSet(updates, "completed_at", tx.CompletedAt)
	putIfSet(updates, "exchange_fee_amount", tx.ExchangeFeeAmount)
	putIfSet(updates, "failure_reason", tx.FailureReason)
	putIfSet(updates, "sender_name", tx.SenderName)
	putIfSet(updates, "sender_reference", tx.SenderReference)
	putIfSet(updates, "payment_rail", tx.PaymentRail)
	putIfSet(updates, "from_address", tx.FromAddress)
	return updates
}

// putIfSet writes column only when the provider reported it: a nil pointer
// leaves what the row already holds.
func putIfSet[T any](updates map[string]any, column string, value *T) {
	if value != nil {
		updates[column] = *value
	}
}

func (r *transactionRepository) UpdateTx(q orm.Query, tx *models.Transaction) error {
	if err := q.Save(tx); err != nil {
		return fmt.Errorf("transaction repo: update: %w", err)
	}
	return nil
}

func (r *transactionRepository) FindByProviderAndDepositIDForUpdate(q orm.Query, provider, depositID string) (*models.Transaction, error) {
	return first[models.Transaction](q.LockForUpdate().
		Where("provider", provider).
		Where("deposit_id", depositID), "transaction repo: find by provider+deposit id for update")
}

func (r *transactionRepository) FindUndeliveredOnramps(providers []string, maxAttempts, limit int) ([]models.Transaction, error) {
	if len(providers) == 0 {
		return nil, nil
	}
	var rows []models.Transaction
	err := facades.Orm().Query().
		Where("type = ?", models.TransactionTypeOnramp).
		Where("status IN ?", []string{
			models.TxStatusProcessing,
			models.TxStatusCompleted,
		}).
		Where("provider IN ?", providers).
		Where("delivery_order_id IS NULL").
		Where("deposit_tx_signature IS NULL").
		Where("delivery_attempts < ?", maxAttempts).
		OrderBy("updated_at").
		Limit(limit).
		Find(&rows)
	if err != nil {
		return nil, fmt.Errorf("transaction repo: find undelivered onramps: %w", err)
	}
	return rows, nil
}

func (r *transactionRepository) FindDeliveriesInFlight(providers []string, updatedBefore time.Time, limit int) ([]models.Transaction, error) {
	if len(providers) == 0 {
		return nil, nil
	}
	var rows []models.Transaction
	err := facades.Orm().Query().
		Where("type = ?", models.TransactionTypeOnramp).
		Where("status NOT IN ?", []string{models.TxStatusCompleted, models.TxStatusFailed, models.TxStatusExpired, models.TxStatusRefunded}).
		Where("provider IN ?", providers).
		Where("delivery_order_id IS NOT NULL").
		Where("delivery_order_id <> ''").
		Where("deposit_tx_signature IS NULL").
		Where("updated_at < ?", updatedBefore).
		OrderBy("updated_at").
		Limit(limit).
		Find(&rows)
	if err != nil {
		return nil, fmt.Errorf("transaction repo: find deliveries in flight: %w", err)
	}
	return rows, nil
}

func (r *transactionRepository) SaveDeliveryOutcome(tx *models.Transaction, previousDeliveryOrderID string) (bool, error) {
	updates := map[string]any{
		"status":            tx.Status,
		"delivery_order_id": tx.DeliveryOrderID,
		"delivery_attempts": tx.DeliveryAttempts,
	}
	putIfSet(updates, "deposit_tx_signature", tx.DepositTxSignature)
	putIfSet(updates, "completed_at", tx.CompletedAt)
	putIfSet(updates, "failure_reason", tx.FailureReason)
	result, err := facades.Orm().Query().
		Model(tx).
		Where("delivery_order_id", previousDeliveryOrderID).
		Where("deposit_tx_signature IS NULL").
		Update(updates)
	if err != nil {
		return false, fmt.Errorf("transaction repo: save delivery outcome: %w", err)
	}
	return result.RowsAffected > 0, nil
}

func (r *transactionRepository) FindStaleRampOrders(updatedBefore time.Time, limit int) ([]models.Transaction, error) {
	var rows []models.Transaction
	err := facades.Orm().Query().Where("(? = '' OR customer_id IN (SELECT id FROM customers WHERE tenant_key=? AND environment=?))", r.tenant, r.tenant, r.environment).Where("(? = 0 OR customer_id = ?)", r.customerID, r.customerID).
		Where("type IN ?", []string{models.TransactionTypeOnramp, models.TransactionTypeOfframp}).
		Where("status NOT IN ?", []string{
			models.TxStatusCompleted, models.TxStatusFailed,
			models.TxStatusExpired, models.TxStatusRefunded,
		}).
		Where("updated_at < ?", updatedBefore).
		// Recovery owns an order whose finalize is in doubt, and a row left in
		// front would starve the limit: finalized and prepared ones stay.
		Where(`NOT EXISTS (SELECT 1 FROM offramp_funding_authorizations a
			WHERE a.transaction_id = transactions.id AND a.state IN ?)`,
			[]string{models.FundingAuthFinalizing, models.FundingAuthUnknown, models.FundingAuthNeedsOperator}).
		Order("updated_at asc").
		Limit(limit).
		Find(&rows)
	if err != nil {
		return nil, fmt.Errorf("transaction repo: find stale ramp orders: %w", err)
	}
	return rows, nil
}

func (r *transactionRepository) FindOpenOfframpsAwaitingSettlement(customerID uint, provider string) ([]models.Transaction, error) {
	var rows []models.Transaction
	if err := openOfframps(facades.Orm().Query(), customerID, provider).Find(&rows); err != nil {
		return nil, fmt.Errorf("transaction repo: find open offramps awaiting settlement: %w", err)
	}
	return rows, nil
}

// openOfframps scopes q to the customer's off-ramps funded or awaiting funds that
// no provider transfer pays out yet, oldest first.
func openOfframps(q orm.Query, customerID uint, provider string) orm.Query {
	return q.Where("customer_id", customerID).
		Where("provider", provider).
		Where("type", models.TransactionTypeOfframp).
		Where("gateway_order_id", "").
		Where("status IN ?", []string{models.TxStatusAwaitingFunds, models.TxStatusFundsReceived}).
		Order("created_at asc").
		Order("id asc")
}

func (r *transactionRepository) ClaimOfframpDeposit(
	customerID uint, provider, txHash string, match func(*models.Transaction) bool,
) (*models.Transaction, error) {
	var claimed *models.Transaction
	err := facades.Orm().Transaction(func(q orm.Query) error {
		var rows []models.Transaction
		if err := openOfframps(q.LockForUpdate(), customerID, provider).Find(&rows); err != nil {
			return err
		}
		// The hash is looked up across every off-ramp of the customer, not only the
		// open ones: an order this deposit funded may have settled since, and a
		// redelivery must find it there rather than claim the next one.
		var owner []models.Transaction
		if err := q.Where("customer_id", customerID).Where("provider", provider).
			Where("type", models.TransactionTypeOfframp).
			Where("deposit_tx_signature", txHash).Limit(1).Find(&owner); err != nil {
			return err
		}
		if len(owner) > 0 {
			claimed = &owner[0]
			return nil
		}
		for i := range rows {
			tx := &rows[i]
			if tx.DepositTxSignature != nil && strings.TrimSpace(*tx.DepositTxSignature) != "" {
				continue
			}
			if !match(tx) {
				continue
			}
			if _, err := q.Model(tx).Update("deposit_tx_signature", txHash); err != nil {
				return err
			}
			tx.DepositTxSignature = &txHash
			claimed = tx
			return nil
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("transaction repo: claim offramp deposit: %w", err)
	}
	return claimed, nil
}

func (r *transactionRepository) FindOnrampsInProviderCustody(customerID uint, provider string) ([]models.Transaction, error) {
	var rows []models.Transaction
	err := facades.Orm().Query().
		Where("customer_id", customerID).
		Where("provider", provider).
		Where("type", models.TransactionTypeOnramp).
		Where("virtual_account_id IS NULL").
		Where("deposit_tx_signature IS NULL").
		// A delivery Ozav closed without a hash has left custody too.
		Where("NOT (status = ? AND delivery_order_id IS NOT NULL AND delivery_order_id <> '')", models.TxStatusCompleted).
		// funds_received is the BRL in, still converting: no tokens yet.
		Where("status IN ?", []string{models.TxStatusProcessing, models.TxStatusCompleted}).
		Find(&rows)
	if err != nil {
		return nil, fmt.Errorf("transaction repo: find onramps in provider custody: %w", err)
	}
	return rows, nil
}

// unclaimedDeposit is the guard of a write that must lose to a deposit claim.
const unclaimedDeposit = "(deposit_tx_signature IS NULL OR deposit_tx_signature = '')"

// claimedOzavOfframp matches a row a deposit has claimed, for a write that
// does not know the row's provider and type: only an Ozav off-ramp is claimed.
const claimedOzavOfframp = "provider = ? AND type = ? AND deposit_tx_signature IS NOT NULL AND deposit_tx_signature <> ''"

// The conditional writes below run on a fresh model keyed by id, not on tx:
// GORM sets the column on the model it is given before the WHERE decides, so a
// write that loses its condition would still leave tx reading as written.
func (r *transactionRepository) ExpireUnfunded(tx *models.Transaction) (bool, error) {
	expired := false
	err := facades.Orm().Transaction(func(q orm.Query) error {
		var err error
		expired, err = expireUnfundedFailingPrepared(q, tx.ID, models.FundingFailureQuoteExpired)
		return err
	})
	if err != nil {
		return false, fmt.Errorf("transaction repo: expire unfunded: %w", err)
	}
	if !expired {
		return false, nil
	}
	tx.Status = models.TxStatusExpired
	return true, nil
}

func (r *transactionRepository) CancelUnfundedOfframp(tx *models.Transaction, reason string) (bool, error) {
	result, err := facades.Orm().Query().Model(&models.Transaction{}).Where("id", tx.ID).
		Where("type", models.TransactionTypeOfframp).
		Where("status", models.TxStatusAwaitingFunds).
		Where(unclaimedDeposit).
		Update(map[string]any{"status": models.TxStatusFailed, "failure_reason": reason})
	if err != nil {
		return false, fmt.Errorf("transaction repo: cancel unfunded offramp: %w", err)
	}
	if result.RowsAffected == 0 {
		return false, nil
	}
	tx.Status = models.TxStatusFailed
	tx.FailureReason = &reason
	return true, nil
}

func (r *transactionRepository) FindByCustomerAndEstimate(customerID uint, provider string, estimateID uint, txType string) (*models.Transaction, error) {
	return first[models.Transaction](facades.Orm().Query().
		Where("customer_id", customerID).
		Where("provider", provider).
		Where("estimate_id", estimateID).
		Where("type", txType), "transaction repo: find by customer+estimate")
}

func (r *transactionRepository) FindByCustomerAndExternalID(customerID uint, externalID string) (*models.Transaction, error) {
	return first[models.Transaction](facades.Orm().Query().
		Where("customer_id", customerID).
		Where("external_id", externalID), "transaction repo: find by customer+external_id")
}

func (r *transactionRepository) FindByExternalID(externalID string) (*models.Transaction, error) {
	// The column is a uuid: anything else cannot name a row, and would be a
	// database error rather than a miss.
	if _, err := uuid.Parse(externalID); err != nil {
		return nil, nil
	}
	return first[models.Transaction](facades.Orm().Query().
		Where("external_id", externalID), "transaction repo: find by external_id")
}

func (r *transactionRepository) FindLatestByCustomer(customerID uint) (*models.Transaction, error) {
	return first[models.Transaction](facades.Orm().Query().
		Where("customer_id", customerID).
		Where("type IN ?", []string{models.TransactionTypeOnramp, models.TransactionTypeOfframp}).
		OrderBy("created_at", "desc").OrderBy("id", "desc"), "transaction repo: find latest by customer")
}

func (r *transactionRepository) FindLatestOfframpDepositAddress(customerID uint, provider, token string) (string, error) {
	tx, err := first[models.Transaction](facades.Orm().Query().
		Where("customer_id", customerID).
		Where("provider", provider).
		Where("type", models.TransactionTypeOfframp).
		Where("target_token", token).
		Where("destination_wallet <> ?", "").
		OrderBy("created_at", "desc").OrderBy("id", "desc"), "transaction repo: find latest offramp deposit address")
	if err != nil || tx == nil {
		return "", err
	}
	return tx.DestinationWallet, nil
}

func (r *transactionRepository) HasOpenByCustomerAndProvider(customerID uint, provider string) (bool, error) {
	return r.HasOpenByCustomerAndProviderTx(facades.Orm().Query(), customerID, provider)
}

// HasOpenByCustomerTx reports, through q, whether the customer has a
// transaction at any provider that has not reached a terminal status: the
// deletion's guard, read inside its own transaction.
func (r *transactionRepository) HasOpenByCustomerTx(q orm.Query, customerID uint) (bool, error) {
	open, err := q.Model(&models.Transaction{}).
		Where("customer_id", customerID).
		Where("status NOT IN ?", models.TerminalTxStatuses).
		Exists()
	if err != nil {
		return false, fmt.Errorf("transaction repo: has open by customer: %w", err)
	}
	return open, nil
}

// HasOpenByCustomerAndProviderTx is HasOpenByCustomerAndProvider through q, for the
// unlink that checks again inside its own transaction.
func (r *transactionRepository) HasOpenByCustomerAndProviderTx(q orm.Query, customerID uint, provider string) (bool, error) {
	open, err := q.Model(&models.Transaction{}).
		Where("customer_id", customerID).
		Where("provider", provider).
		Where("status NOT IN ?", models.TerminalTxStatuses).
		Exists()
	if err != nil {
		return false, fmt.Errorf("transaction repo: has open by customer and provider: %w", err)
	}
	return open, nil
}

func (r *transactionRepository) Paginate(filters TransactionListFilters) ([]models.Transaction, int64, error) {
	q := facades.Orm().Query().Model(&models.Transaction{}).Where("customer_id", filters.CustomerID)
	if filters.Type != "" {
		q = q.Where("type", filters.Type)
	} else if filters.RampOnly {
		q = q.Where("type IN ?", []string{models.TransactionTypeOnramp, models.TransactionTypeOfframp})
	}
	if filters.Status != "" {
		q = q.Where("status", filters.Status)
	}
	if filters.From != nil {
		q = q.Where("created_at >= ?", *filters.From)
	}
	if filters.To != nil {
		q = q.Where("created_at < ?", *filters.To)
	}
	q = q.OrderBy("created_at", "desc").OrderBy("id", "desc")

	var txs []models.Transaction
	var total int64
	if err := q.Paginate(filters.Page, filters.PerPage, &txs, &total); err != nil {
		return nil, 0, fmt.Errorf("transaction repo: paginate: %w", err)
	}
	return txs, total, nil
}

func (r *transactionRepository) AdminPaginate(filters AdminTransactionListFilters) ([]models.Transaction, int64, error) {
	q := facades.Orm().Query().Model(&models.Transaction{}).With("User")
	if filters.Provider != "" {
		q = q.Where("provider", filters.Provider)
	}
	if filters.Type != "" {
		q = q.Where("type", filters.Type)
	}
	if filters.Status != "" {
		q = q.Where("status", filters.Status)
	}
	if filters.Rail != "" {
		q = q.Where("rail", filters.Rail)
	}
	if filters.CustomerID != 0 {
		q = q.Where("customer_id", filters.CustomerID)
	}
	if filters.UserID != 0 {
		q = q.Where("user_id", filters.UserID)
	}
	if filters.Search != "" {
		like := likeContains(filters.Search)
		// external_id is a uuid column; cast to text so ILIKE applies.
		q = q.Where("external_id::text ILIKE ? OR gateway_order_id ILIKE ?", like, like)
	}
	if filters.From != nil {
		q = q.Where("created_at >= ?", *filters.From)
	}
	if filters.To != nil {
		q = q.Where("created_at < ?", *filters.To)
	}
	column, dir := adminTransactionSort.parse(filters.Sort)
	q = q.OrderBy(column, dir).OrderBy("id", "desc")

	var txs []models.Transaction
	var total int64
	if err := q.Paginate(filters.Page, filters.PerPage, &txs, &total); err != nil {
		return nil, 0, fmt.Errorf("transaction repo: admin paginate: %w", err)
	}
	return txs, total, nil
}

func (r *transactionRepository) FindByID(id uint) (*models.Transaction, error) {
	return first[models.Transaction](facades.Orm().Query().With("User").Where("id", id), "transaction repo: find by id")
}

var adminTransactionSort = sortable{
	columns: map[string]string{"created_at": "created_at"},
	column:  "created_at",
	dir:     "desc",
}

func (r *transactionRepository) ListByStatus(status string, limit int) ([]models.Transaction, error) {
	var rows []models.Transaction
	err := facades.Orm().Query().Where("(? = '' OR customer_id IN (SELECT id FROM customers WHERE tenant_key=? AND environment=?))", r.tenant, r.tenant, r.environment).Where("(? = 0 OR customer_id = ?)", r.customerID, r.customerID).
		Where("status = ?", status).
		Order("updated_at asc").Order("id asc").
		Limit(limit).Find(&rows)
	if err != nil {
		return nil, fmt.Errorf("transaction repo: list by status: %w", err)
	}
	return rows, nil
}

func (r *transactionRepository) ListReturningBefore(cutoff time.Time, limit int) ([]models.Transaction, error) {
	var rows []models.Transaction
	err := facades.Orm().Query().Where("(? = '' OR customer_id IN (SELECT id FROM customers WHERE tenant_key=? AND environment=?))", r.tenant, r.tenant, r.environment).Where("(? = 0 OR customer_id = ?)", r.customerID, r.customerID).
		Where("status = ?", models.TxStatusReturning).
		Where("created_at < ?", cutoff).
		Order("created_at asc").Order("id asc").
		Limit(limit).Find(&rows)
	if err != nil {
		return nil, fmt.Errorf("transaction repo: list returning before: %w", err)
	}
	return rows, nil
}

func (r *transactionRepository) ListWithAttention(limit int) ([]models.Transaction, error) {
	var rows []models.Transaction
	err := facades.Orm().Query().Where("(? = '' OR customer_id IN (SELECT id FROM customers WHERE tenant_key=? AND environment=?))", r.tenant, r.tenant, r.environment).Where("(? = 0 OR customer_id = ?)", r.customerID, r.customerID).
		Where("attention_code IS NOT NULL").
		Order("attention_at asc").Order("id asc").
		Limit(limit).Find(&rows)
	if err != nil {
		return nil, fmt.Errorf("transaction repo: list with attention: %w", err)
	}
	return rows, nil
}

func NewTransactionRepositoryForTenant(tenant, environment string) TransactionRepository {
	return &transactionRepository{tenant: tenant, environment: environment}
}

// NewTransactionRepositoryForHolder filters attention rows before the per-source limit.
func NewTransactionRepositoryForHolder(tenant, environment string, customerID uint) TransactionRepository {
	return &transactionRepository{tenant: tenant, environment: environment, customerID: customerID}
}
