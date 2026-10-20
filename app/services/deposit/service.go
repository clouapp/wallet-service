package deposit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	contractscache "github.com/goravel/framework/contracts/cache"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/blockheight"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/deposit/pending"
	"github.com/macrowallets/waas/app/services/depositrecord"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/pkg/types"
)

// RedisStore runs the scanner's checkpoint cache and watched-address commands.
// The service keeps the keys. A nil RedisStore means Redis is not configured.
type RedisStore interface {
	// Uint64 reads key; found is false, with no error, when the key does not exist.
	Uint64(ctx context.Context, key string) (value uint64, found bool, err error)
	Set(ctx context.Context, key string, value uint64, expiration time.Duration) error
	// Delete removes key; a key that does not exist is not an error.
	Delete(ctx context.Context, key string) error
	SIsMember(ctx context.Context, key, member string) (bool, error)
	SCard(ctx context.Context, key string) (int64, error)
	SMIsMember(ctx context.Context, key string, members ...any) ([]bool, error)
	ReplaceSet(ctx context.Context, key string, members ...any) error
}

// ---------------------------------------------------------------------------
// Service — stateless deposit scanner designed for Lambda invocation.
// Each call to ScanLatestBlocks processes new blocks since last checkpoint.
// State lives in Postgres (checkpoint, transactions); Redis caches the checkpoint
// and the watched addresses and holds the per-chain scan lock.
// ---------------------------------------------------------------------------

type Service struct {
	store                RedisStore
	checkpoints          CheckpointStore
	cache                contractscache.Driver
	cacheInSync          sync.Map // chain IDs whose Redis checkpoint this process knows matches the database
	registry             *chain.Registry
	webhookSvc           *webhook.Service
	addressRepo          *repositories.AddressRepository
	txRepo               transactionStore
	blockHeightProviders map[string]blockheight.Provider
	heightFailures       map[string]int
	withdrawals          WithdrawalConfirmations
	deposits             DepositEvents
	balances             WalletBalanceRefresher
	scanMu               sync.Mutex // guards scan and scanFallback; chains are scanned in parallel
	scan                 ScanOptions
	scanFallback         ScanOptions
	scanSource           scanOptionSource
	chainScan            chainScanOverrideSource
	failure              FailurePolicy
	pending              pending.Store
	sleep                func(ctx context.Context, d time.Duration) error
	now                  func() time.Time
	// staleAfter is how long an outbound transfer may stay unsettled (stale_outbound.go).
	staleAfter time.Duration
	staleCache staleOutboundCache
	// reconcileWithdrawals and reconcileTransactions are the rows withdrawals:reconcile
	// reads (withdrawal_reconcile.go).
	reconcileWithdrawals  WithdrawalFinder
	reconcileTransactions TransactionFinder
}

// transactionStore is the deposit rows the scanner writes and the tracker updates.
// Within (depositrecord.Store) opens one transaction, which the queries made with its
// callback context join: a deposit's record, a sweep's confirmation and its
// sweep.confirmed webhook row.
type transactionStore interface {
	depositrecord.Store
	CountInternalTransfers(ctx context.Context, chainID, txHash string, walletID uuid.UUID) (int64, error)
	FindPendingByChain(ctx context.Context, chainID string) ([]models.Transaction, error)
	FindConfirmedOutboundWithoutFee(ctx context.Context, chainID string, limit int) ([]models.Transaction, error)
	FindConfirmedDepositsMissingEvent(ctx context.Context, eventType string, from, to time.Time, chainIDs []string, limit int) ([]models.Transaction, error)
	FindConfirmedNativeDeposits(ctx context.Context, chainID string, since time.Time, afterID uuid.UUID, limit int) ([]models.Transaction, error)
	SetBlockNumber(ctx context.Context, id uuid.UUID, block uint64) error
	// SetBlock records the block number, hash and transfer position of a deposit a
	// reorganization moved, or one recorded without a hash.
	SetBlock(ctx context.Context, id uuid.UUID, block uint64, blockHash string, position int) error
	SetFee(ctx context.Context, id uuid.UUID, fee string) error
	// RecordConfirmations writes a row still pending or confirming only
	// (models.ErrTransactionSettled otherwise).
	RecordConfirmations(ctx context.Context, id uuid.UUID, confirmations int, status string, confirmedAt *time.Time) error
	MarkFailed(ctx context.Context, id uuid.UUID, reason string) error
	// MarkFailedFrom fails a row still in fromStatus (models.ErrTransactionSettled otherwise).
	MarkFailedFrom(ctx context.Context, id uuid.UUID, reason, fromStatus string) error
	FindByID(ctx context.Context, id uuid.UUID) (*models.Transaction, error)
	// SetRawPayload rewrites raw_payload (what the tracker keeps between passes).
	SetRawPayload(ctx context.Context, id uuid.UUID, rawPayload string) error
}

// DepositEvents publishes deposit webhooks scoped to the wallet's account with the
// amount in base units and as a decimal. Republish sends again an event that
// occurred at occurredAt to the configs that existed then and never got it.
type DepositEvents interface {
	Publish(ctx context.Context, eventType types.EventType, tx models.Transaction) error
	Republish(ctx context.Context, eventType types.EventType, tx models.Transaction, occurredAt time.Time) error
}

// SetDepositEvents wires the deposit webhook publisher. Without it no deposit webhook
// is sent: the raw transaction carries base units without decimals and reaches every
// account, so there is no safe fallback.
func (s *Service) SetDepositEvents(deposits DepositEvents) {
	s.deposits = deposits
}

func (s *Service) publishDeposit(ctx context.Context, eventType types.EventType, tx models.Transaction) {
	if s.deposits == nil {
		slog.Error("deposit webhook not sent: no deposit events publisher configured", "event_type", eventType, "transaction_id", tx.ID)
		return
	}
	if err := s.deposits.Publish(ctx, eventType, tx); err != nil {
		slog.Error("deposit webhook not sent", "event_type", eventType, "transaction_id", tx.ID, "error", err)
	}
}

// WithdrawalConfirmations publishes withdrawal.confirmed once a withdrawal transaction
// reaches its required confirmations, and repairs confirmations whose event was lost.
type WithdrawalConfirmations interface {
	MarkConfirmed(ctx context.Context, tx *models.Transaction) error
	Backfill(ctx context.Context, limit int) (int, error)
}

// SetWithdrawalConfirmations wires the withdrawal lifecycle publisher. Without it the
// tracker falls back to the legacy raw-transaction withdrawal.confirmed event.
func (s *Service) SetWithdrawalConfirmations(withdrawals WithdrawalConfirmations) {
	s.withdrawals = withdrawals
}

// Deps is everything the deposit service needs. A nil field means that
// dependency is absent; without Checkpoints or Cache ScanLatestBlocks refuses to scan.
type Deps struct {
	Store                RedisStore
	Checkpoints          CheckpointStore
	Cache                contractscache.Driver
	Registry             *chain.Registry
	Webhook              *webhook.Service
	Addresses            *repositories.AddressRepository
	Transactions         *repositories.TransactionRepository
	BlockHeightProviders map[string]blockheight.Provider
}

// NewService wires the deposit service from Deps.
func NewService(deps Deps) *Service {
	return &Service{
		store:                deps.Store,
		checkpoints:          deps.Checkpoints,
		cache:                deps.Cache,
		registry:             deps.Registry,
		webhookSvc:           deps.Webhook,
		addressRepo:          deps.Addresses,
		txRepo:               deps.Transactions,
		blockHeightProviders: deps.BlockHeightProviders,
		heightFailures:       make(map[string]int),
		scan:                 DefaultScanOptions(),
		scanFallback:         DefaultScanOptions(),
		failure:              DefaultFailurePolicy(),
		sleep:                sleepContext,
		now:                  func() time.Time { return time.Now().UTC() },
	}
}

// ScanLatestBlocks is the Lambda entry point. Scans new blocks for a chain.
// Called by EventBridge on schedule (every 5-60s depending on chain). A cycle that
// finds another scanner on the chain is skipped and returns nil; one whose checkpoint
// is far past the head fails with ErrCheckpointAheadOfHead and scans nothing.
func (s *Service) ScanLatestBlocks(ctx context.Context, chainID string) error {
	err := s.scanLatestBlocks(ctx, chainID)
	if errors.Is(err, ErrScanInProgress) {
		slog.Info("deposit scan skipped, another scanner holds the chain", "chain", chainID)
		return nil
	}
	return err
}

// scanLatestBlocks runs one cycle under the chain's scan lock, or returns
// ErrScanInProgress when another scanner holds it. A checkpoint that cannot be read
// skips the cycle with an error and moves nothing.
func (s *Service) scanLatestBlocks(ctx context.Context, chainID string) error {
	opts := s.scanOptionsFor(ctx, chainID)

	adapter, err := s.registry.Chain(chainID)
	if err != nil {
		return err
	}

	release, err := s.lockChain(chainID)
	if err != nil {
		return err
	}
	defer release()

	lastBlock, found, err := s.loadCheckpoint(ctx, chainID)
	if err != nil {
		return fmt.Errorf("deposit scan skipped: %w", err)
	}
	if !found {
		lastBlock, err = adapter.GetLatestBlock(ctx)
		if err != nil {
			return fmt.Errorf("get latest block: %w", err)
		}
		slog.Info("first scan, starting from current", "chain", chainID, "block", lastBlock)
		return s.saveCheckpoint(ctx, chainID, lastBlock)
	}

	latestBlock, err := adapter.GetLatestBlock(ctx)
	if err != nil {
		return fmt.Errorf("get latest block: %w", err)
	}

	if latestBlock <= lastBlock {
		// Nothing new: a checkpoint slightly ahead (a lagging node) is waited out, one
		// far past the head (another network, a hand edit) fails the cycle loudly.
		return checkCheckpointNotAhead(chainID, lastBlock, latestBlock)
	}

	lag := latestBlock - lastBlock
	endBlock := lastBlock + opts.window(lag)
	if lag > opts.BatchBlocks {
		slog.Warn("deposit scanner behind the head, catching up", "chain", chainID, "checkpoint", lastBlock, "head", latestBlock, "lag", lag, "window", endBlock-lastBlock, "concurrency", opts.Concurrency)
	}

	slog.Info("scanning blocks", "chain", chainID, "from", lastBlock+1, "to", endBlock, "concurrency", opts.Concurrency)

	scannedTo, scanErr := s.scanRange(ctx, chainID, adapter, lastBlock+1, endBlock, opts.Concurrency)
	if scannedTo > lastBlock {
		if err := s.saveCheckpoint(ctx, chainID, scannedTo); err != nil {
			slog.Error("save checkpoint failed", "chain", chainID, "error", err)
		}
	}
	if scanErr != nil {
		slog.Error("process block failed", "chain", chainID, "checkpoint", scannedTo, "error", scanErr)
		return scanErr
	}

	if err := s.updateConfirmations(ctx, chainID, adapter, latestBlock); err != nil {
		slog.Error("update confirmations failed", "chain", chainID, "error", err)
	}

	slog.Info("scan complete", "chain", chainID, "processed", endBlock-lastBlock, "head", latestBlock)
	return nil
}

// processTransfer records a transfer to a watched address as a pending deposit and
// reports whether it created one. A transfer already recorded (by this scanner, by
// the provider push or by another process at the same time) is left as it is
// (depositrecord.Record); siblings are the transfers of its transaction this scan
// records with it.
func (s *Service) processTransfer(ctx context.Context, chainID string, adapter types.Chain, transfer types.DetectedTransfer, siblings []depositrecord.Transfer) (bool, error) {
	watched, err := s.isWatchedAddress(ctx, chainID, transfer.To)
	if err != nil {
		return false, classify(pending.ClassDatabase, err)
	}
	if !watched {
		return false, nil
	}

	addr, err := s.addressRepo.FindByChainAndAddress(ctx, chainID, transfer.To)
	if err != nil && !errors.Is(err, models.ErrRepositoryNotFound) {
		return false, classify(pending.ClassDatabase, fmt.Errorf("lookup address: %w", err))
	}
	if err != nil || addr == nil {
		slog.Warn("watched-address cache lists an address the database does not hold; skipping the transfer", "chain", chainID, "address", transfer.To, "tx", transfer.TxHash)
		return false, nil
	}

	internal, err := s.txRepo.CountInternalTransfers(ctx, chainID, transfer.TxHash, addr.WalletID)
	if err != nil {
		return false, classify(pending.ClassDatabase, fmt.Errorf("check internal transfer: %w", err))
	}
	if internal > 0 {
		slog.Info("transfer is a sweep or gas seed of the wallet, not a deposit", "chain", chainID, "tx", transfer.TxHash, "to", transfer.To)
		return false, nil
	}

	asset := adapter.NativeAsset()
	var tokenContract string
	if transfer.Token != nil {
		asset = transfer.Token.Symbol
		tokenContract = transfer.Token.Contract
	}

	tx := &models.Transaction{
		ID:             uuid.New(),
		AddressID:      &addr.ID,
		WalletID:       addr.WalletID,
		ExternalUserID: addr.ExternalUserID,
		Chain:          chainID,
		TxType:         models.TxTypeDeposit,
		TxHash:         transfer.TxHash,
		LogIndex:       transfer.LogIndex,
		FromAddress:    transfer.From,
		ToAddress:      transfer.To,
		DestinationTag: transfer.DestinationTag,
		Amount:         transfer.Amount.String(),
		Asset:          asset,
		TokenContract:  tokenContract,
		Confirmations:  0,
		RequiredConfs:  int(adapter.RequiredConfirmations()),
		Finality:       chain.FinalityPolicyOf(adapter),
		Status:         string(types.TxStatusPending),
		BlockNumber:    int64(transfer.BlockNumber),
		BlockHash:      transfer.BlockHash,
		Direction:      models.TxDirectionInbound,
		Source:         models.TxSourceChain,
		RawPayload:     depositPayload(transfer),
	}

	outcome, err := depositrecord.Record(ctx, s.txRepo, tx, siblings)
	if err != nil {
		return false, classify(pending.ClassDatabase, fmt.Errorf("record deposit: %w", err))
	}
	if outcome != depositrecord.Created {
		depositrecord.LogSkipped(outcome, tx)
		return false, nil
	}

	s.publishDeposit(ctx, types.EventDepositPending, *tx)

	slog.Info("deposit detected", "chain", chainID, "tx", transfer.TxHash, "log_index", transfer.LogIndex, "user", addr.ExternalUserID, "asset", asset, "amount", transfer.Amount.String())
	return true, nil
}

// ---------------------------------------------------------------------------
// Watched-address cache
// ---------------------------------------------------------------------------

func addressCacheKey(chainID string) string {
	return "vault:addresses:" + chainID
}

// isWatchedAddress answers from the Redis set; when Redis is absent or fails it asks
// the database, so an unavailable cache never drops a deposit silently.
func (s *Service) isWatchedAddress(ctx context.Context, chainID, address string) (bool, error) {
	if address == "" {
		return false, nil
	}
	if s.store != nil {
		isMember, err := s.store.SIsMember(ctx, addressCacheKey(chainID), address)
		if err == nil {
			return isMember, nil
		}
		slog.Warn("address cache unavailable, checking the database", "chain", chainID, "error", err)
	}
	count, err := s.addressRepo.CountByChainAndAddress(ctx, chainID, address)
	if err != nil {
		return false, fmt.Errorf("lookup watched address: %w", err)
	}
	return count > 0, nil
}

// RefreshAddressCache reloads monitored addresses into Redis.
// Called after generating new addresses.
func (s *Service) RefreshAddressCache(ctx context.Context, chainID string) error {
	if s.store == nil {
		return nil
	}
	addresses, err := s.addressRepo.PluckActiveAddresses(ctx, chainID)
	if err != nil {
		return err
	}
	return s.replaceAddressCache(ctx, chainID, addresses)
}

// SyncAddressCache rebuilds the chain's watched-address set when it does not hold
// exactly the active addresses in the database. Another process sharing Redis can
// overwrite the set, and every missing member is a deposit the scanner would skip.
// It reports whether the set was rebuilt.
func (s *Service) SyncAddressCache(ctx context.Context, chainID string) (bool, error) {
	if s.store == nil {
		return false, nil
	}
	addresses, err := s.addressRepo.PluckActiveAddresses(ctx, chainID)
	if err != nil {
		return false, fmt.Errorf("load active %s addresses: %w", chainID, err)
	}
	inSync, cached, err := s.addressCacheMatches(ctx, chainID, addresses)
	if err != nil {
		return false, err
	}
	if inSync {
		return false, nil
	}
	if err := s.replaceAddressCache(ctx, chainID, addresses); err != nil {
		return false, err
	}
	slog.Warn("address cache was stale, rebuilt from the database", "chain", chainID, "cached", cached, "active", len(addresses))
	return true, nil
}

func (s *Service) addressCacheMatches(ctx context.Context, chainID string, addresses []string) (bool, int64, error) {
	key := addressCacheKey(chainID)
	cached, err := s.store.SCard(ctx, key)
	if err != nil {
		return false, 0, fmt.Errorf("count cached %s addresses: %w", chainID, err)
	}
	if cached != int64(len(distinct(addresses))) {
		return false, cached, nil
	}
	if len(addresses) == 0 {
		return true, cached, nil
	}
	present, err := s.store.SMIsMember(ctx, key, toMembers(addresses)...)
	if err != nil {
		return false, cached, fmt.Errorf("check cached %s addresses: %w", chainID, err)
	}
	for _, isMember := range present {
		if !isMember {
			return false, cached, nil
		}
	}
	return true, cached, nil
}

// replaceAddressCache swaps the set in one MULTI/EXEC so a concurrent scan never sees
// it empty; an empty address list clears a stale set instead of leaving it behind.
func (s *Service) replaceAddressCache(ctx context.Context, chainID string, addresses []string) error {
	return s.store.ReplaceSet(ctx, addressCacheKey(chainID), toMembers(addresses)...)
}

func toMembers(addresses []string) []interface{} {
	members := make([]interface{}, len(addresses))
	for i, address := range addresses {
		members[i] = address
	}
	return members
}

func distinct(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}
