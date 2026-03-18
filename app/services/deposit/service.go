package deposit

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"

	"github.com/macromarkets/vault/app/models"
	"github.com/macromarkets/vault/app/services/chain"
	"github.com/macromarkets/vault/app/services/webhook"
	"github.com/macromarkets/vault/pkg/types"
)

// ---------------------------------------------------------------------------
// Service — stateless deposit scanner designed for Lambda invocation.
// Each call to ScanLatestBlocks processes new blocks since last checkpoint.
// State lives in Redis (checkpoint) and Postgres (transactions).
// ---------------------------------------------------------------------------

type Service struct {
	db         *sqlx.DB
	rdb        *redis.Client
	registry   *chain.Registry
	webhookSvc *webhook.Service
}

func NewService(db *sqlx.DB, rdb *redis.Client, registry *chain.Registry, webhookSvc *webhook.Service) *Service {
	return &Service{db: db, rdb: rdb, registry: registry, webhookSvc: webhookSvc}
}

// ScanLatestBlocks is the Lambda entry point. Scans new blocks for a chain.
// Called by EventBridge on schedule (every 5-60s depending on chain).
func (s *Service) ScanLatestBlocks(ctx context.Context, chainID string) error {
	adapter, err := s.registry.Chain(chainID)
	if err != nil {
		return err
	}

	// 1. Load checkpoint (last processed block)
	lastBlock, err := s.loadCheckpoint(ctx, chainID)
	if err != nil {
		// First run: start from current block
		lastBlock, err = adapter.GetLatestBlock(ctx)
		if err != nil {
			return fmt.Errorf("get latest block: %w", err)
		}
		slog.Info("first scan, starting from current", "chain", chainID, "block", lastBlock)
		return s.saveCheckpoint(ctx, chainID, lastBlock)
	}

	// 2. Get current chain head
	latestBlock, err := adapter.GetLatestBlock(ctx)
	if err != nil {
		return fmt.Errorf("get latest block: %w", err)
	}

	if latestBlock <= lastBlock {
		return nil // no new blocks
	}

	// 3. Cap blocks per invocation to avoid Lambda timeout
	maxBlocks := uint64(50)
	endBlock := lastBlock + maxBlocks
	if endBlock > latestBlock {
		endBlock = latestBlock
	}

	slog.Info("scanning blocks", "chain", chainID, "from", lastBlock+1, "to", endBlock)

	// 4. Process each block
	for blockNum := lastBlock + 1; blockNum <= endBlock; blockNum++ {
		if err := s.processBlock(ctx, chainID, adapter, blockNum); err != nil {
			slog.Error("process block failed", "chain", chainID, "block", blockNum, "error", err)
			// Save checkpoint at last successful block
			s.saveCheckpoint(ctx, chainID, blockNum-1)
			return err
		}
	}

	// 5. Save checkpoint
	if err := s.saveCheckpoint(ctx, chainID, endBlock); err != nil {
		slog.Error("save checkpoint failed", "chain", chainID, "error", err)
	}

	// 6. Update confirmations for pending deposits
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
			// Continue — don't fail the whole block for one bad tx
		}
	}
	return nil
}

func (s *Service) processTransfer(ctx context.Context, chainID string, adapter types.Chain, transfer types.DetectedTransfer) error {
	// 1. Check if destination is a monitored address (Redis SET)
	if s.rdb != nil {
		isMine, err := s.rdb.SIsMember(ctx, "vault:addresses:"+chainID, transfer.To).Result()
		if err != nil || !isMine {
			return nil // not our address
		}
	} else {
		// Fallback: check DB directly
		var count int
		if err := s.db.GetContext(ctx, &count, "SELECT COUNT(*) FROM addresses WHERE chain = $1 AND address = $2", chainID, transfer.To); err != nil || count == 0 {
			return nil
		}
	}

	// 2. Lookup address → user mapping
	var addr models.Address
	if err := s.db.GetContext(ctx, &addr, "SELECT * FROM addresses WHERE chain = $1 AND address = $2", chainID, transfer.To); err != nil {
		return fmt.Errorf("lookup address: %w", err)
	}

	// 3. Dedup by tx_hash + chain
	var exists int
	if err := s.db.GetContext(ctx, &exists, "SELECT COUNT(*) FROM transactions WHERE chain = $1 AND tx_hash = $2 AND tx_type = 'deposit'", chainID, transfer.TxHash); err != nil {
		return err
	}
	if exists > 0 {
		return nil
	}

	// 4. Determine asset
	asset := adapter.NativeAsset()
	var tokenContract string
	if transfer.Token != nil {
		asset = transfer.Token.Symbol
		tokenContract = transfer.Token.Contract
	}

	// 5. Insert transaction
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
		// CreatedAt and UpdatedAt handled by orm.Model
	}

	if _, err := s.db.NamedExecContext(ctx, `
		INSERT INTO transactions (id, address_id, wallet_id, external_user_id, chain, tx_type, tx_hash,
			from_address, to_address, amount, asset, token_contract, confirmations, required_confs,
			status, block_number, block_hash, created_at, updated_at)
		VALUES (:id, :address_id, :wallet_id, :external_user_id, :chain, :tx_type, :tx_hash,
			:from_address, :to_address, :amount, :asset, :token_contract, :confirmations, :required_confs,
			:status, :block_number, :block_hash, NOW(), NOW())`, tx); err != nil {
		return fmt.Errorf("insert tx: %w", err)
	}

	// 6. Fire webhook
	s.webhookSvc.EnqueueEvent(ctx, tx.ID, types.EventDepositPending, tx)

	slog.Info("deposit detected", "chain", chainID, "tx", transfer.TxHash, "user", addr.ExternalUserID, "asset", asset, "amount", transfer.Amount.String())
	return nil
}

func (s *Service) updateConfirmations(ctx context.Context, chainID string, adapter types.Chain, currentBlock uint64) error {
	var pending []models.Transaction
	if err := s.db.SelectContext(ctx, &pending, `
		SELECT * FROM transactions WHERE chain = $1 AND tx_type = 'deposit' AND status IN ('pending', 'confirming')`, chainID); err != nil {
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

		if _, err := s.db.ExecContext(ctx, `UPDATE transactions SET confirmations = $1, status = $2, confirmed_at = $3 WHERE id = $4`,
			confs, newStatus, confirmedAt, tx.ID); err != nil {
			slog.Error("update confs", "tx_id", tx.ID, "error", err)
			continue
		}

		// Fire webhook on state change
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
	var addresses []string
	if err := s.db.SelectContext(ctx, &addresses, "SELECT address FROM addresses WHERE chain = $1 AND is_active = true", chainID); err != nil {
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
	_, err := pipe.Exec(ctx)
	return err
}
