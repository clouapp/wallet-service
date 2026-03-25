package deposit

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
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
	rdb         *redis.Client
	registry    *chain.Registry
	webhookSvc  *webhook.Service
	addressRepo repositories.AddressRepository
	txRepo      repositories.TransactionRepository
}

func NewService(rdb *redis.Client, registry *chain.Registry, webhookSvc *webhook.Service, addressRepo repositories.AddressRepository, txRepo repositories.TransactionRepository) *Service {
	return &Service{rdb: rdb, registry: registry, webhookSvc: webhookSvc, addressRepo: addressRepo, txRepo: txRepo}
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

	maxBlocks := uint64(50)
	endBlock := lastBlock + maxBlocks
	if endBlock > latestBlock {
		endBlock = latestBlock
	}

	slog.Info("scanning blocks", "chain", chainID, "from", lastBlock+1, "to", endBlock)

	for blockNum := lastBlock + 1; blockNum <= endBlock; blockNum++ {
		if err := s.processBlock(ctx, chainID, adapter, blockNum); err != nil {
			slog.Error("process block failed", "chain", chainID, "block", blockNum, "error", err)
			s.saveCheckpoint(ctx, chainID, blockNum-1)
			return err
		}
	}

	if err := s.saveCheckpoint(ctx, chainID, endBlock); err != nil {
		slog.Error("save checkpoint failed", "chain", chainID, "error", err)
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

	for _, transfer := range transfers {
		if err := s.processTransfer(ctx, chainID, adapter, transfer); err != nil {
			slog.Error("process transfer", "tx", transfer.TxHash, "error", err)
		}
	}
	return nil
}

func (s *Service) processTransfer(ctx context.Context, chainID string, adapter types.Chain, transfer types.DetectedTransfer) error {
	if s.rdb != nil {
		isMine, err := s.rdb.SIsMember(ctx, "vault:addresses:"+chainID, transfer.To).Result()
		if err != nil || !isMine {
			return nil
		}
	} else {
		count, err := s.addressRepo.CountByChainAndAddress(chainID, transfer.To)
		if err != nil || count == 0 {
			return nil
		}
	}

	addr, err := s.addressRepo.FindByChainAndAddress(chainID, transfer.To)
	if err != nil || addr == nil {
		return fmt.Errorf("lookup address: %w", err)
	}

	exists, err := s.txRepo.CountByChainAndTxHash(chainID, transfer.TxHash, "deposit")
	if err != nil {
		return err
	}
	if exists > 0 {
		return nil
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
		TxType:         "deposit",
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
	}

	if err := s.txRepo.Create(tx); err != nil {
		return fmt.Errorf("insert tx: %w", err)
	}

	s.webhookSvc.EnqueueEvent(ctx, tx.ID, types.EventDepositPending, tx)

	slog.Info("deposit detected", "chain", chainID, "tx", transfer.TxHash, "user", addr.ExternalUserID, "asset", asset, "amount", transfer.Amount.String())
	return nil
}

func (s *Service) updateConfirmations(ctx context.Context, chainID string, adapter types.Chain, currentBlock uint64) error {
	pending, err := s.txRepo.FindPendingByChain(chainID)
	if err != nil {
		return err
	}

	for _, tx := range pending {
		if tx.BlockNumber == 0 {
			continue
		}
		confs := int(currentBlock) - int(tx.BlockNumber)
		if confs < 0 {
			confs = 0
		}

		newStatus := string(types.TxStatusConfirming)
		var confirmedAt *time.Time
		if confs >= tx.RequiredConfs {
			newStatus = string(types.TxStatusConfirmed)
			now := time.Now().UTC()
			confirmedAt = &now
		}

		if err := s.txRepo.UpdateFields(tx.ID, map[string]interface{}{
			"confirmations": confs,
			"status":        newStatus,
			"confirmed_at":  confirmedAt,
		}); err != nil {
			slog.Error("update confs", "tx_id", tx.ID, "error", err)
			continue
		}

		if newStatus == string(types.TxStatusConfirmed) && tx.Status != string(types.TxStatusConfirmed) {
			s.webhookSvc.EnqueueEvent(ctx, tx.ID, types.EventDepositConfirmed, tx)
		} else if tx.Status == string(types.TxStatusPending) && newStatus == string(types.TxStatusConfirming) {
			s.webhookSvc.EnqueueEvent(ctx, tx.ID, types.EventDepositConfirming, tx)
		}
	}
	return nil
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
	if len(addresses) == 0 {
		return nil
	}
	key := "vault:addresses:" + chainID
	pipe := s.rdb.Pipeline()
	pipe.Del(ctx, key)
	members := make([]interface{}, len(addresses))
	for i, a := range addresses {
		members[i] = a
	}
	pipe.SAdd(ctx, key, members...)
	_, err = pipe.Exec(ctx)
	return err
}
