package repositories

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories/internal/db"
	"github.com/macrowallets/waas/pkg/amount"
	"github.com/macrowallets/waas/pkg/types"
)

// TransactionRepository loads and updates transaction rows.
type TransactionRepository struct {
	db.Base
}

// NewTransactionRepository builds a repository. Nil uses a fresh query per call.
func NewTransactionRepository(query orm.Query) *TransactionRepository {
	return &TransactionRepository{Base: db.NewBase(query)}
}

// Within runs fn inside one transaction. Queries made with the callback
// context join that transaction, including a webhook event for the same sweep leg.
func (r *TransactionRepository) Within(ctx context.Context, fn func(context.Context) error) error {
	if fn == nil {
		return fmt.Errorf("transaction: callback is required")
	}
	return r.Transaction(ctx, func(tx orm.Query) error {
		return fn(db.WithTx(ctx, tx))
	})
}

// Create inserts a transaction after rejecting a negative amount or fee.
func (r *TransactionRepository) Create(ctx context.Context, tx *models.Transaction) error {
	if tx == nil {
		return fmt.Errorf("transaction is required")
	}
	if err := tx.ValidateAmounts(); err != nil {
		return err
	}
	if err := r.Query(ctx).Create(tx); err != nil {
		return fmt.Errorf("create transaction: %w", err)
	}
	return nil
}

// FindByID returns one transaction.
func (r *TransactionRepository) FindByID(ctx context.Context, id uuid.UUID) (*models.Transaction, error) {
	var tx models.Transaction
	if err := r.Query(ctx).Find(&tx, id); err != nil {
		return nil, fmt.Errorf("find transaction: %w", err)
	}
	if tx.ID == uuid.Nil {
		return nil, models.ErrRepositoryNotFound
	}
	return &tx, nil
}

// FindByIDForAccount returns the transaction when it belongs to a wallet of
// accountID. A missing row and a transaction on another account are both
// ErrRepositoryNotFound.
func (r *TransactionRepository) FindByIDForAccount(ctx context.Context, id, accountID uuid.UUID) (*models.Transaction, error) {
	if id == uuid.Nil || accountID == uuid.Nil {
		return nil, models.ErrRepositoryNotFound
	}
	var tx models.Transaction
	err := r.Query(ctx).
		Where("id = ? AND wallet_id IN (SELECT id FROM wallets WHERE account_id = ?)", id, accountID).
		FirstOrFail(&tx)
	if err != nil {
		return nil, db.LookupError(err, "find transaction")
	}
	return &tx, nil
}

// FindByIDAndWallet returns the transaction when it belongs to walletID.
func (r *TransactionRepository) FindByIDAndWallet(ctx context.Context, txID string, walletID uuid.UUID) (*models.Transaction, error) {
	var tx models.Transaction
	if err := r.Query(ctx).Where("id = ? AND wallet_id = ?", txID, walletID).FirstOrFail(&tx); err != nil {
		return nil, db.LookupError(err, "find transaction")
	}
	return &tx, nil
}

// FindByIdempotencyKey returns the transaction stored under a withdrawal idempotency key.
func (r *TransactionRepository) FindByIdempotencyKey(ctx context.Context, key string) (*models.Transaction, error) {
	var tx models.Transaction
	if err := r.Query(ctx).Where("idempotency_key", key).FirstOrFail(&tx); err != nil {
		return nil, db.LookupError(err, "find transaction by idempotency key")
	}
	return &tx, nil
}

// FindByWallet lists transactions for a wallet, optionally filtered by type and status.
func (r *TransactionRepository) FindByWallet(ctx context.Context, walletID uuid.UUID, txType, status string, limit, offset int) ([]models.Transaction, int64, error) {
	countQuery := r.Query(ctx).Model(&models.Transaction{}).Where("wallet_id = ?", walletID)
	dataQuery := r.Query(ctx).Where("wallet_id = ?", walletID)
	if txType != "" {
		countQuery = countQuery.Where("tx_type = ?", txType)
		dataQuery = dataQuery.Where("tx_type = ?", txType)
	}
	if status != "" {
		countQuery = countQuery.Where("status = ?", status)
		dataQuery = dataQuery.Where("status = ?", status)
	}
	total, err := countQuery.Count()
	if err != nil {
		return nil, 0, fmt.Errorf("count transactions: %w", err)
	}
	var transactions []models.Transaction
	if err := dataQuery.Offset(offset).Limit(limit).Find(&transactions); err != nil {
		return nil, 0, fmt.Errorf("list transactions: %w", err)
	}
	return transactions, total, nil
}

// FindByChainAndTxHash returns the transaction for a chain and hash.
func (r *TransactionRepository) FindByChainAndTxHash(ctx context.Context, chainID, txHash string) (*models.Transaction, error) {
	var tx models.Transaction
	if err := r.Query(ctx).Where("chain = ? AND tx_hash = ?", chainID, txHash).FirstOrFail(&tx); err != nil {
		return nil, db.LookupError(err, "find transaction by hash")
	}
	return &tx, nil
}

// CountByChainAndTxHash counts rows for a chain, hash, and type (another outbound
// row of ours carrying a hash: the inputs proof's copy check).
func (r *TransactionRepository) CountByChainAndTxHash(ctx context.Context, chainID, txHash, txType string) (int64, error) {
	count, err := r.Query(ctx).Model(&models.Transaction{}).
		Where("chain", chainID).
		Where("tx_hash", txHash).
		Where("tx_type", txType).
		Count()
	if err != nil {
		return 0, fmt.Errorf("count transactions by hash: %w", err)
	}
	return count, nil
}

// LockTransactionDeposits takes the advisory lock of one chain transaction's
// deposits, held until the database transaction of ctx ends (Within): the deposit
// recorders of the scanner and the push decide on a transaction one at a time. Called
// outside a transaction, the lock ends with the statement.
func (r *TransactionRepository) LockTransactionDeposits(ctx context.Context, chainID, txHash string) error {
	key := "deposits/" + chainID + "/" + txHashForms(txHash)[0]
	if _, err := r.Query(ctx).Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", key); err != nil {
		return fmt.Errorf("lock transaction deposits: %w", err)
	}
	return nil
}

// txHashForms are the spellings of one transaction hash: a hex hash (0x-prefixed, or
// 32 bytes without a prefix) in lower and upper case, the lower first, as hex
// compares without case (deposit.sameTxHash); any other hash (a base58 signature) as
// it is.
func txHashForms(txHash string) []string {
	body := strings.TrimPrefix(strings.TrimPrefix(txHash, "0x"), "0X")
	isHex := body != "" && strings.Trim(strings.ToLower(body), "0123456789abcdef") == ""
	if !isHex || (body == txHash && len(body) != 64) {
		return []string{txHash}
	}
	lower := strings.ToLower(txHash)
	if lower == strings.ToUpper(txHash) {
		return []string{lower}
	}
	upper := strings.ToUpper(txHash)
	if strings.HasPrefix(lower, "0x") {
		upper = "0x" + strings.ToUpper(body)
	}
	return []string{lower, upper, txHash}
}

// FindDepositsByChainAndTxHash returns the deposit rows of one transaction, ordered by
// position (log_index); a hex hash matches in either case (txHashForms).
func (r *TransactionRepository) FindDepositsByChainAndTxHash(ctx context.Context, chainID, txHash string) ([]models.Transaction, error) {
	var deposits []models.Transaction
	forms := txHashForms(txHash)
	spellings := make([]any, len(forms))
	for i, form := range forms {
		spellings[i] = form
	}
	err := r.Query(ctx).
		Where("chain", chainID).
		WhereIn("tx_hash", spellings).
		Where("tx_type", models.TxTypeDeposit).
		Order("log_index").
		Find(&deposits)
	if err != nil {
		return nil, fmt.Errorf("list deposits of transaction: %w", err)
	}
	return deposits, nil
}

// FillDepositBlockHash writes blockHash on deposit id when the row holds none (a
// provider push that sent no block hash); a hash that is there is kept.
func (r *TransactionRepository) FillDepositBlockHash(ctx context.Context, id uuid.UUID, blockHash string) error {
	if id == uuid.Nil {
		return fmt.Errorf("transaction id is required")
	}
	if _, err := r.Query(ctx).Model(&models.Transaction{}).
		Where("id", id).
		Where("tx_type", models.TxTypeDeposit).
		Where("block_hash", "").
		Update("block_hash", blockHash); err != nil {
		return fmt.Errorf("fill deposit block hash: %w", err)
	}
	return nil
}

// ClaimDepositPosition moves a deposit recorded before positions existed (log_index
// types.NativeValuePosition) to position and reports false when the row is not such
// a deposit any more. A position another deposit of the transaction holds is a
// unique violation.
func (r *TransactionRepository) ClaimDepositPosition(ctx context.Context, id uuid.UUID, position int) (bool, error) {
	if id == uuid.Nil {
		return false, fmt.Errorf("transaction id is required")
	}
	result, err := r.Query(ctx).Model(&models.Transaction{}).
		Where("id", id).
		Where("tx_type", models.TxTypeDeposit).
		Where("log_index", types.NativeValuePosition).
		Update("log_index", position)
	if err != nil {
		return false, fmt.Errorf("claim deposit position: %w", err)
	}
	return result.RowsAffected == 1, nil
}

// internalTransferTxTypes move funds between addresses of one wallet: a sweep
// (child to base) and the gas seed that pays for it (base to child). Withdrawals are not
// listed: one that reaches a watched address is a real deposit for its owner.
var internalTransferTxTypes = []string{models.TxTypeSweep, models.TxTypeGasSeed}

// CountInternalTransfers counts the sweeps and gas seeds the wallet recorded for this
// transaction, so the deposit scanners do not record the same move again as a deposit.
func (r *TransactionRepository) CountInternalTransfers(ctx context.Context, chainID, txHash string, walletID uuid.UUID) (int64, error) {
	if chainID == "" || txHash == "" || walletID == uuid.Nil {
		return 0, fmt.Errorf("chain, transaction hash and wallet are required to look up internal transfers")
	}
	count, err := r.Query(ctx).
		Model(&models.Transaction{}).
		Where("chain", chainID).
		Where("tx_hash", txHash).
		Where("wallet_id", walletID).
		WhereIn("tx_type", []any{models.TxTypeSweep, models.TxTypeGasSeed}).
		Count()
	if err != nil {
		return 0, fmt.Errorf("count internal transfers: %w", err)
	}
	return count, nil
}

// FindPendingByChain returns every transaction on chainID that the confirmation
// loop must advance — deposits plus outbound legs (withdrawals, sweeps, gas seeds).
func (r *TransactionRepository) FindPendingByChain(ctx context.Context, chainID string) ([]models.Transaction, error) {
	var pending []models.Transaction
	err := r.Query(ctx).
		Where("chain", chainID).
		WhereIn("tx_type", []any{
			models.TxTypeDeposit,
			models.TxTypeWithdrawal,
			models.TxTypeSweep,
			models.TxTypeGasSeed,
		}).
		WhereIn("status", []any{string(types.TxStatusPending), string(types.TxStatusConfirming)}).
		Find(&pending)
	if err != nil {
		return nil, fmt.Errorf("list pending transactions: %w", err)
	}
	return pending, nil
}

// CountStaleOutbound counts, per chain, the withdrawals, sweeps and gas seeds still
// pending or confirming that were recorded before olderThan.
func (r *TransactionRepository) CountStaleOutbound(ctx context.Context, olderThan time.Time) (map[string]int, error) {
	var rows []struct {
		Chain string
		Count int
	}
	if err := r.Query(ctx).Raw(
		`SELECT chain, COUNT(*) AS count FROM transactions
		 WHERE tx_type IN (?, ?, ?) AND status IN (?, ?) AND created_at < ?
		 GROUP BY chain`,
		models.TxTypeWithdrawal, models.TxTypeSweep, models.TxTypeGasSeed,
		string(types.TxStatusPending), string(types.TxStatusConfirming), olderThan.UTC(),
	).Scan(&rows); err != nil {
		return nil, fmt.Errorf("count stale outbound transactions: %w", err)
	}
	counts := make(map[string]int, len(rows))
	for _, row := range rows {
		counts[row.Chain] = row.Count
	}
	return counts, nil
}

// FindConfirmedOutboundWithoutFee lists confirmed withdrawals, sweeps and gas seeds
// of chainID whose paid fee was never recorded, oldest first.
func (r *TransactionRepository) FindConfirmedOutboundWithoutFee(ctx context.Context, chainID string, limit int) ([]models.Transaction, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("list transactions without fee: limit is required")
	}
	var rows []models.Transaction
	err := r.Query(ctx).
		Where("chain", chainID).
		WhereIn("tx_type", []any{models.TxTypeWithdrawal, models.TxTypeSweep, models.TxTypeGasSeed}).
		Where("status", string(types.TxStatusConfirmed)).
		Where("tx_hash <> ''").
		Where("(fee IS NULL OR fee = '')").
		Order("created_at").
		Limit(limit).
		Find(&rows)
	if err != nil {
		return nil, fmt.Errorf("list transactions without fee: %w", err)
	}
	return rows, nil
}

// FindConfirmedDepositsMissingEvent lists the deposits confirmed between from and to
// (on chainIDs; none means every chain) that at least one webhook config should have
// received eventType for and never did, oldest confirmation first. A config counts
// when it is active, subscribes to eventType, may see the deposit's wallet (its
// wallet, its account, or a legacy config without either) and was created no later
// than the confirmation (a config without a creation time counts as older); it never
// got the event when it has no webhook_events row for
// (config, eventType, deposit id). These are the rules webhook.EnqueueScoped applies
// to an event with OccurredAt, so republishing a returned deposit fills the gap. It
// fits only events whose subject is the deposit id: deposit.confirming carries the
// count in its subject ("<id>:<confirmations>") and is never backfilled.
func (r *TransactionRepository) FindConfirmedDepositsMissingEvent(ctx context.Context, eventType string, from, to time.Time, chainIDs []string, limit int) ([]models.Transaction, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("list deposits missing an event: limit is required")
	}
	if eventType == "" {
		return nil, fmt.Errorf("list deposits missing an event: event type is required")
	}
	query := r.Query(ctx).
		Where("tx_type = ? AND status = ?", models.TxTypeDeposit, string(types.TxStatusConfirmed)).
		Where("confirmed_at >= ? AND confirmed_at <= ?", from, to).
		Where(`EXISTS (
			SELECT 1 FROM webhook_configs c
			JOIN wallets w ON w.id = transactions.wallet_id
			WHERE c.is_active
			  AND position(? IN c.events) > 0
			  AND (c.wallet_id IS NULL OR c.wallet_id = transactions.wallet_id)
			  AND (c.account_id IS NULL OR c.account_id = w.account_id)
			  AND (c.created_at IS NULL OR c.created_at <= (transactions.confirmed_at AT TIME ZONE 'UTC'))
			  AND NOT EXISTS (
				SELECT 1 FROM webhook_events e
				WHERE e.webhook_config_id = c.id AND e.event_type = ? AND e.subject_id = transactions.id::text
			  )
		)`, `"`+eventType+`"`, eventType)
	if len(chainIDs) > 0 {
		chains := make([]any, len(chainIDs))
		for i, chainID := range chainIDs {
			chains[i] = chainID
		}
		query = query.WhereIn("chain", chains)
	}
	var rows []models.Transaction
	if err := query.Order("confirmed_at").Order("id").Limit(limit).Find(&rows); err != nil {
		return nil, fmt.Errorf("list deposits missing an event: %w", err)
	}
	return rows, nil
}

// FindConfirmedNativeDeposits lists the confirmed deposits of chainID in the
// chain's native asset (no token contract) that have a transaction hash, recorded
// at or after since (a zero since reads them all), in id order after afterID
// (uuid.Nil starts at the first). deposits:verify-receipts pages through them with
// the last id of each page.
func (r *TransactionRepository) FindConfirmedNativeDeposits(ctx context.Context, chainID string, since time.Time, afterID uuid.UUID, limit int) ([]models.Transaction, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("list confirmed native deposits: limit is required")
	}
	query := r.Query(ctx).
		Where("chain = ? AND tx_type = ? AND status = ?", chainID, models.TxTypeDeposit, string(types.TxStatusConfirmed)).
		Where("(token_contract IS NULL OR token_contract = '')").
		Where("tx_hash <> ''")
	if !since.IsZero() {
		query = query.Where("created_at >= (?::timestamptz AT TIME ZONE 'UTC')", since.UTC())
	}
	if afterID != uuid.Nil {
		query = query.Where("id > ?", afterID)
	}
	var rows []models.Transaction
	if err := query.Order("id").Limit(limit).Find(&rows); err != nil {
		return nil, fmt.Errorf("list confirmed native deposits: %w", err)
	}
	return rows, nil
}

// SetFee stores the paid fee in native base units.
func (r *TransactionRepository) SetFee(ctx context.Context, id uuid.UUID, fee string) error {
	return r.updateColumns(ctx, id, map[string]any{"fee": fee}, "set transaction fee")
}

// SetBlockNumber sets transactions.block_number.
func (r *TransactionRepository) SetBlockNumber(ctx context.Context, id uuid.UUID, block uint64) error {
	return r.updateColumns(ctx, id, map[string]any{"block_number": block}, "set transaction block number")
}

// SetBlock records the block a deposit is in, its number and hash, and its
// transfer's position in it (log_index: a block-wide position, an EVM or TRON log
// index, differs from one block to another): a deposit a reorganization moved to
// another block, or one recorded without a hash. A position another deposit of the
// transaction holds is a unique violation.
func (r *TransactionRepository) SetBlock(ctx context.Context, id uuid.UUID, block uint64, blockHash string, position int) error {
	return r.updateColumns(ctx, id, map[string]any{"block_number": block, "block_hash": blockHash, "log_index": position}, "set transaction block")
}

// MarkFailed sets the failed status and the reason (an on-chain result code or
// "expired") in error_message, and releases the idempotency key so a retry of the
// withdrawal with the same key can record its new transaction under it.
func (r *TransactionRepository) MarkFailed(ctx context.Context, id uuid.UUID, reason string) error {
	return r.updateColumns(ctx, id, map[string]any{
		"status":          string(types.TxStatusFailed),
		"error_message":   reason,
		"idempotency_key": nil,
	}, "mark transaction failed")
}

// RecordBroadcast stores what a send answered on a row recorded before it: the hash
// the node returned and the raw payload (an unknown outcome is kept there). r.Query
// joins the caller's transaction.
func (r *TransactionRepository) RecordBroadcast(ctx context.Context, id uuid.UUID, txHash, rawPayload string) error {
	if strings.TrimSpace(txHash) == "" {
		return fmt.Errorf("record transaction broadcast: tx hash is required")
	}
	return r.updateColumns(ctx, id, map[string]any{"tx_hash": txHash, "raw_payload": rawPayload}, "record transaction broadcast")
}

// SetRawPayload rewrites raw_payload: what the confirmation tracker keeps between
// passes (a first sight of an expiry, the next try of a proof).
func (r *TransactionRepository) SetRawPayload(ctx context.Context, id uuid.UUID, rawPayload string) error {
	return r.updateColumns(ctx, id, map[string]any{"raw_payload": rawPayload}, "set transaction raw payload")
}

// RecordConfirmations stores the confirmation count, the status and confirmed_at of
// a row still pending or confirming; a row another process settled (confirmed or
// failed) since it was read is left as it is: models.ErrTransactionSettled.
func (r *TransactionRepository) RecordConfirmations(ctx context.Context, id uuid.UUID, confirmations int, status string, confirmedAt *time.Time) error {
	return r.updateWhileIn(ctx, id, []any{string(types.TxStatusPending), string(types.TxStatusConfirming)}, map[string]any{
		"confirmations": confirmations,
		"status":        status,
		"confirmed_at":  confirmedAt,
	}, "record transaction confirmations")
}

// MarkFailedFrom is MarkFailed of a row still in fromStatus, the status the caller
// read: a row another process settled meanwhile is left as it is
// (models.ErrTransactionSettled).
func (r *TransactionRepository) MarkFailedFrom(ctx context.Context, id uuid.UUID, reason, fromStatus string) error {
	return r.updateWhileIn(ctx, id, []any{fromStatus}, map[string]any{
		"status":          string(types.TxStatusFailed),
		"error_message":   reason,
		"idempotency_key": nil,
	}, "mark transaction failed")
}

// reorgMarkPayloadKey is the raw_payload key the confirmation tracker sets on a
// deposit whose block was reorganized away while it is unresolved.
const reorgMarkPayloadKey = "reorg_seen_at"

// CountUnresolvedReorgs counts, per chain, the pending or confirming deposits whose
// block was reorganized away and that nothing has resolved yet.
func (r *TransactionRepository) CountUnresolvedReorgs(ctx context.Context) (map[string]int, error) {
	var rows []struct {
		Chain string
		Count int
	}
	if err := r.Query(ctx).Raw(
		`SELECT chain, COUNT(*) AS count FROM transactions
		 WHERE tx_type = ? AND status IN (?, ?) AND jsonb_exists(raw_payload, ?)
		 GROUP BY chain`,
		models.TxTypeDeposit, string(types.TxStatusPending), string(types.TxStatusConfirming), reorgMarkPayloadKey,
	).Scan(&rows); err != nil {
		return nil, fmt.Errorf("count unresolved reorganized deposits: %w", err)
	}
	counts := make(map[string]int, len(rows))
	for _, row := range rows {
		counts[row.Chain] = row.Count
	}
	return counts, nil
}

// FindUnresolvedReorgs lists the unresolved reorganized deposits of chainID (every
// chain when empty), oldest first, at most limit.
func (r *TransactionRepository) FindUnresolvedReorgs(ctx context.Context, chainID string, limit int) ([]models.Transaction, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("list unresolved reorganized deposits: limit is required")
	}
	query := r.Query(ctx).
		Where("tx_type", models.TxTypeDeposit).
		WhereIn("status", []any{string(types.TxStatusPending), string(types.TxStatusConfirming)}).
		Where("jsonb_exists(raw_payload, ?)", reorgMarkPayloadKey)
	if chainID != "" {
		query = query.Where("chain", chainID)
	}
	var rows []models.Transaction
	if err := query.Order("created_at ASC").Order("id ASC").Limit(limit).Find(&rows); err != nil {
		return nil, fmt.Errorf("list unresolved reorganized deposits: %w", err)
	}
	return rows, nil
}

// SetConfirmations sets transactions.confirmations.
func (r *TransactionRepository) SetConfirmations(ctx context.Context, id uuid.UUID, confirmations int) error {
	return r.updateColumns(ctx, id, map[string]any{"confirmations": confirmations}, "set transaction confirmations")
}

// SetAmount sets transactions.amount.
func (r *TransactionRepository) SetAmount(ctx context.Context, id uuid.UUID, value string) error {
	return r.updateColumns(ctx, id, map[string]any{"amount": value}, "set transaction amount")
}

// SetIdempotencyKey sets transactions.idempotency_key.
func (r *TransactionRepository) SetIdempotencyKey(ctx context.Context, id uuid.UUID, key string) error {
	return r.updateColumns(ctx, id, map[string]any{"idempotency_key": key}, "set transaction idempotency key")
}

// List returns transactions matching the optional filters, newest first.
func (r *TransactionRepository) List(ctx context.Context, chainID, txType, status, userID string, limit, offset int) ([]models.Transaction, int64, error) {
	if limit <= 0 {
		limit = 50
	}
	countQuery := r.Query(ctx).Model(&models.Transaction{})
	dataQuery := r.Query(ctx)
	countQuery, dataQuery = applyTransactionFilters(countQuery, dataQuery, chainID, txType, status, userID)
	return listTransactions(countQuery, dataQuery, limit, offset, "created_at DESC")
}

// ListForAccount is the account-scoped variant of List used by the external API.
func (r *TransactionRepository) ListForAccount(ctx context.Context, accountID uuid.UUID, chainID, txType, status, userID string, limit, offset int) ([]models.Transaction, int64, error) {
	if limit <= 0 {
		limit = 50
	}
	scope := "wallet_id IN (SELECT id FROM wallets WHERE account_id = ?)"
	countQuery := r.Query(ctx).Model(&models.Transaction{}).Where(scope, accountID)
	dataQuery := r.Query(ctx).Where(scope, accountID)
	countQuery, dataQuery = applyTransactionFilters(countQuery, dataQuery, chainID, txType, status, userID)
	return listTransactions(countQuery, dataQuery, limit, offset, "created_at DESC")
}

// ListChangedForAccount returns up to limit transactions of the account's wallets whose
// (updated_at, id) comes strictly after (afterUpdatedAt, afterID), in that order, and how
// many match from that position on. The nil id makes the position inclusive of
// afterUpdatedAt. The filters are those of ListForAccount. A limit below one is an error.
func (r *TransactionRepository) ListChangedForAccount(ctx context.Context, accountID uuid.UUID, chainID, txType, status, userID string, afterUpdatedAt time.Time, afterID uuid.UUID, limit int) ([]models.Transaction, int64, error) {
	if limit <= 0 {
		return nil, 0, fmt.Errorf("list changed transactions: limit is required")
	}
	scope := "wallet_id IN (SELECT id FROM wallets WHERE account_id = ?)"
	after := "(updated_at, id) > (?, ?)"
	since := afterUpdatedAt.UTC()
	countQuery := r.Query(ctx).Model(&models.Transaction{}).Where(scope, accountID).Where(after, since, afterID)
	dataQuery := r.Query(ctx).Where(scope, accountID).Where(after, since, afterID)
	countQuery, dataQuery = applyTransactionFilters(countQuery, dataQuery, chainID, txType, status, userID)
	return listTransactions(countQuery, dataQuery, limit, 0, "updated_at ASC, id ASC")
}

// ListByWalletAndChain lists one wallet's transactions on a chain, newest block first.
func (r *TransactionRepository) ListByWalletAndChain(ctx context.Context, walletID uuid.UUID, chainID string, limit, offset int) ([]models.Transaction, int64, error) {
	if limit <= 0 {
		limit = 50
	}
	countQuery := r.Query(ctx).Model(&models.Transaction{}).Where("wallet_id = ?", walletID).Where("chain = ?", chainID)
	dataQuery := r.Query(ctx).Where("wallet_id = ?", walletID).Where("chain = ?", chainID)
	return listTransactions(countQuery, dataQuery, limit, offset, "block_number DESC, created_at DESC")
}

// updateWhileIn writes columns to the row only while its status is one of statuses;
// a row in another status is models.ErrTransactionSettled.
func (r *TransactionRepository) updateWhileIn(ctx context.Context, id uuid.UUID, statuses []any, columns map[string]any, op string) error {
	if id == uuid.Nil {
		return fmt.Errorf("transaction id is required")
	}
	result, err := r.Query(ctx).Model(&models.Transaction{}).Where("id", id).WhereIn("status", statuses).Update(columns)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("%s %s: %w", op, id, models.ErrTransactionSettled)
	}
	return nil
}

func (r *TransactionRepository) updateColumns(ctx context.Context, id uuid.UUID, columns map[string]any, op string) error {
	if id == uuid.Nil {
		return fmt.Errorf("transaction id is required")
	}
	if err := amount.RequireNonNegativeColumns(columns, models.TransactionAmountColumns...); err != nil {
		return err
	}
	if _, err := r.Query(ctx).Model(&models.Transaction{}).Where("id", id).Update(columns); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	return nil
}

func applyTransactionFilters(countQuery, dataQuery orm.Query, chainID, txType, status, userID string) (orm.Query, orm.Query) {
	if chainID != "" {
		countQuery = countQuery.Where("chain", chainID)
		dataQuery = dataQuery.Where("chain", chainID)
	}
	if txType != "" {
		countQuery = countQuery.Where("tx_type", txType)
		dataQuery = dataQuery.Where("tx_type", txType)
	}
	if status != "" {
		countQuery = countQuery.Where("status", status)
		dataQuery = dataQuery.Where("status", status)
	}
	if userID != "" {
		countQuery = countQuery.Where("external_user_id", userID)
		dataQuery = dataQuery.Where("external_user_id", userID)
	}
	return countQuery, dataQuery
}

func listTransactions(countQuery, dataQuery orm.Query, limit, offset int, order string) ([]models.Transaction, int64, error) {
	total, err := countQuery.Count()
	if err != nil {
		return nil, 0, fmt.Errorf("count transactions: %w", err)
	}
	var txs []models.Transaction
	if err := dataQuery.Order(order).Limit(limit).Offset(offset).Find(&txs); err != nil {
		return nil, 0, fmt.Errorf("list transactions: %w", err)
	}
	return txs, total, nil
}
