package deposit

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/blockheight"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/pkg/types"
)

// ---------------------------------------------------------------------------
// Service — stateless deposit scanner designed for Lambda invocation.
// Each call to ScanLatestBlocks processes new blocks since last checkpoint.
// State lives in Redis (checkpoint) and Postgres (transactions).
// ---------------------------------------------------------------------------

type Service struct {
	rdb                  *redis.Client
	registry             *chain.Registry
	webhookSvc           *webhook.Service
	addressRepo          repositories.AddressRepository
	txRepo               repositories.TransactionRepository
	blockHeightProviders map[string]blockheight.Provider
	heightFailures       map[string]int
	withdrawals          WithdrawalConfirmations
	deposits             DepositEvents
	scan                 ScanOptions
}

// DepositEvents publishes deposit webhooks scoped to the wallet's account with the
// amount in base units and as a decimal.
type DepositEvents interface {
	Publish(ctx context.Context, eventType types.EventType, tx models.Transaction) error
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

func NewService(rdb *redis.Client, registry *chain.Registry, webhookSvc *webhook.Service, addressRepo repositories.AddressRepository, txRepo repositories.TransactionRepository, blockHeightProviders map[string]blockheight.Provider) *Service {
	return &Service{
		rdb:                  rdb,
		registry:             registry,
		webhookSvc:           webhookSvc,
		addressRepo:          addressRepo,
		txRepo:               txRepo,
		blockHeightProviders: blockHeightProviders,
		heightFailures:       make(map[string]int),
		scan:                 DefaultScanOptions(),
	}
}

// ScanLatestBlocks is the Lambda entry point. Scans new blocks for a chain.
// Called by EventBridge on schedule (every 5-60s depending on chain).
func (s *Service) ScanLatestBlocks(ctx context.Context, chainID string) error {
	adapter, err := s.registry.Chain(chainID)
	if err != nil {
		return err
	}

	lastBlock, err := s.loadCheckpoint(ctx, chainID)
	if err != nil {
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
		return nil
	}

	lag := latestBlock - lastBlock
	endBlock := lastBlock + s.scanWindow(lag)
	if lag > s.scan.BatchBlocks {
		slog.Warn("deposit scanner behind the head, catching up", "chain", chainID, "checkpoint", lastBlock, "head", latestBlock, "lag", lag, "window", endBlock-lastBlock, "concurrency", s.scan.Concurrency)
	}

	slog.Info("scanning blocks", "chain", chainID, "from", lastBlock+1, "to", endBlock)

	scannedTo, scanErr := s.scanRange(ctx, chainID, adapter, lastBlock+1, endBlock)
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

func (s *Service) processBlock(ctx context.Context, chainID string, adapter types.Chain, blockNum uint64) error {
	transfers, err := adapter.ScanBlock(ctx, blockNum)
	if err != nil {
		return err
	}
	s.recordTransfers(ctx, chainID, adapter, transfers)
	return nil
}

// processTransfer records a transfer to a watched address as a pending deposit and
// reports whether it created one; a transaction already recorded is left as it is.
func (s *Service) processTransfer(ctx context.Context, chainID string, adapter types.Chain, transfer types.DetectedTransfer) (bool, error) {
	watched, err := s.isWatchedAddress(ctx, chainID, transfer.To)
	if err != nil {
		return false, err
	}
	if !watched {
		return false, nil
	}

	addr, err := s.addressRepo.FindByChainAndAddress(chainID, transfer.To)
	if err != nil || addr == nil {
		return false, fmt.Errorf("lookup address: %w", err)
	}

	exists, err := s.txRepo.CountByChainAndTxHash(chainID, transfer.TxHash, models.TxTypeDeposit)
	if err != nil {
		return false, err
	}
	if exists > 0 {
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
		FromAddress:    transfer.From,
		ToAddress:      transfer.To,
		Amount:         transfer.Amount.String(),
		Asset:          asset,
		TokenContract:  tokenContract,
		Confirmations:  0,
		RequiredConfs:  int(adapter.RequiredConfirmations()),
		Status:         string(types.TxStatusPending),
		BlockNumber:    int64(transfer.BlockNumber),
		BlockHash:      transfer.BlockHash,
		Direction:      models.TxDirectionInbound,
		Source:         models.TxSourceChain,
		RawPayload:     "{}",
	}

	if err := s.txRepo.Create(tx); err != nil {
		return false, fmt.Errorf("insert tx: %w", err)
	}

	s.publishDeposit(ctx, types.EventDepositPending, *tx)

	slog.Info("deposit detected", "chain", chainID, "tx", transfer.TxHash, "user", addr.ExternalUserID, "asset", asset, "amount", transfer.Amount.String())
	return true, nil
}

// ---------------------------------------------------------------------------
// Redis checkpoint
// ---------------------------------------------------------------------------

func (s *Service) loadCheckpoint(ctx context.Context, chainID string) (uint64, error) {
	if s.rdb == nil {
		return 0, fmt.Errorf("no redis")
	}
	return s.rdb.Get(ctx, "vault:checkpoint:"+chainID).Uint64()
}

func (s *Service) saveCheckpoint(ctx context.Context, chainID string, blockNum uint64) error {
	if s.rdb == nil {
		return nil
	}
	return s.rdb.Set(ctx, "vault:checkpoint:"+chainID, blockNum, 0).Err()
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
	if s.rdb != nil {
		isMember, err := s.rdb.SIsMember(ctx, addressCacheKey(chainID), address).Result()
		if err == nil {
			return isMember, nil
		}
		slog.Warn("address cache unavailable, checking the database", "chain", chainID, "error", err)
	}
	count, err := s.addressRepo.CountByChainAndAddress(chainID, address)
	if err != nil {
		return false, fmt.Errorf("lookup watched address: %w", err)
	}
	return count > 0, nil
}

// RefreshAddressCache reloads monitored addresses into Redis.
// Called after generating new addresses.
func (s *Service) RefreshAddressCache(ctx context.Context, chainID string) error {
	if s.rdb == nil {
		return nil
	}
	addresses, err := s.addressRepo.PluckActiveAddresses(chainID)
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
	if s.rdb == nil {
		return false, nil
	}
	addresses, err := s.addressRepo.PluckActiveAddresses(chainID)
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
	cached, err := s.rdb.SCard(ctx, key).Result()
	if err != nil {
		return false, 0, fmt.Errorf("count cached %s addresses: %w", chainID, err)
	}
	if cached != int64(len(distinct(addresses))) {
		return false, cached, nil
	}
	if len(addresses) == 0 {
		return true, cached, nil
	}
	present, err := s.rdb.SMIsMember(ctx, key, toMembers(addresses)...).Result()
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
	key := addressCacheKey(chainID)
	pipe := s.rdb.TxPipeline()
	pipe.Del(ctx, key)
	if len(addresses) > 0 {
		pipe.SAdd(ctx, key, toMembers(addresses)...)
	}
	_, err := pipe.Exec(ctx)
	return err
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
