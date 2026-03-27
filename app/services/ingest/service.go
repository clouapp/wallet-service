package ingest

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/ingest/providers"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/pkg/types"
)

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

func (s *Service) ProcessTransfers(ctx context.Context, chainID string, transfers []providers.InboundTransfer) error {
	adapter, err := s.registry.Chain(chainID)
	if err != nil {
		return fmt.Errorf("unknown chain %s: %w", chainID, err)
	}

	for _, transfer := range transfers {
		if err := s.processTransfer(ctx, chainID, adapter, transfer); err != nil {
			slog.Error("ingest process transfer", "tx", transfer.TxHash, "error", err)
		}
	}
	return nil
}

func (s *Service) processTransfer(ctx context.Context, chainID string, adapter types.Chain, transfer providers.InboundTransfer) error {
	if transfer.Amount == nil {
		return fmt.Errorf("missing amount")
	}

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

	exists, err := s.txRepo.CountByChainTxHashAndLogIndex(chainID, transfer.TxHash, transfer.LogIndex, "deposit")
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
	} else if transfer.Asset != "" {
		asset = transfer.Asset
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
		LogIndex:       transfer.LogIndex,
	}

	if err := s.txRepo.Create(tx); err != nil {
		return fmt.Errorf("insert tx: %w", err)
	}

	s.webhookSvc.EnqueueEvent(ctx, tx.ID, types.EventDepositPending, tx)

	slog.Info("ingest deposit", "chain", chainID, "tx", transfer.TxHash, "log_index", transfer.LogIndex, "user", addr.ExternalUserID, "asset", asset, "amount", transfer.Amount.String())
	return nil
}
