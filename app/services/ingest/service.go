package ingest

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/event"
	"github.com/goravel/framework/facades"
	"github.com/redis/go-redis/v9"

	"github.com/macrowallets/waas/app/events"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/ingest/providers"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/app/services/withdraw"
	"github.com/macrowallets/waas/pkg/types"
)

// addressReader is the address lookup ingest uses to attribute a transfer.
type addressReader interface {
	CountByChainAndAddress(ctx context.Context, chainID, address string) (int64, error)
	FindByChainAndAddress(ctx context.Context, chainID, address string) (*models.Address, error)
}

type Service struct {
	rdb         *redis.Client
	registry    *chain.Registry
	webhookSvc  *webhook.Service
	addressRepo addressReader
	txRepo      repositories.TransactionRepository
	deposits    DepositEvents
}

// DepositEvents publishes deposit webhooks scoped to the wallet's account with the
// amount in base units and as a decimal.
type DepositEvents interface {
	Publish(ctx context.Context, eventType types.EventType, tx models.Transaction) error
}

// SetDepositEvents wires the deposit webhook publisher; without it no deposit webhook is sent.
func (s *Service) SetDepositEvents(deposits DepositEvents) {
	s.deposits = deposits
}

func NewService(rdb *redis.Client, registry *chain.Registry, webhookSvc *webhook.Service, addressRepo addressReader, txRepo repositories.TransactionRepository) *Service {
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
	if transfer.Amount == nil && !transfer.AmountIsHuman {
		return fmt.Errorf("missing amount")
	}

	if s.rdb != nil {
		isMine, err := s.rdb.SIsMember(ctx, "vault:addresses:"+chainID, transfer.To).Result()
		if err != nil || !isMine {
			return nil
		}
	} else {
		count, err := s.addressRepo.CountByChainAndAddress(ctx, chainID, transfer.To)
		if err != nil || count == 0 {
			return nil
		}
	}

	addr, err := s.addressRepo.FindByChainAndAddress(ctx, chainID, transfer.To)
	if err != nil || addr == nil {
		return fmt.Errorf("lookup address: %w", err)
	}

	exists, err := s.txRepo.CountByChainTxHashAndLogIndex(chainID, transfer.TxHash, transfer.LogIndex, models.TxTypeDeposit)
	if err != nil {
		return err
	}
	if exists > 0 {
		return nil
	}

	asset := adapter.NativeAsset()
	var tokenContract string
	if transfer.Token != nil {
		seeded, findErr := s.registry.FindTokenByContract(chainID, transfer.Token.Contract)
		if findErr != nil {
			return nil
		}
		asset = seeded.Symbol
		tokenContract = seeded.Contract
		transfer.Token.Symbol = seeded.Symbol
		transfer.Token.Decimals = seeded.Decimals
		if transfer.AmountIsHuman {
			base, convErr := withdraw.ResolveWithdrawalAmount(chainID, adapter.NativeAsset(), 0, seeded.Symbol, transfer.HumanAmount, []types.Token{*seeded})
			if convErr != nil {
				return convErr
			}
			transfer.Amount = base.BaseUnits
		}
	} else if transfer.Asset != "" {
		asset = types.CanonicalAssetSymbol(transfer.Asset)
	}
	if transfer.Amount == nil {
		return fmt.Errorf("missing amount")
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
		LogIndex:       transfer.LogIndex,
	}

	if err := s.txRepo.Create(tx); err != nil {
		return fmt.Errorf("insert tx: %w", err)
	}

	s.publishDepositPending(ctx, *tx)

	if ev := facades.Event(); ev != nil {
		_ = ev.Job(&events.DepositDetected{}, []event.Arg{
			{Type: "string", Value: tx.WalletID.String()},
			{Type: "string", Value: chainID},
			{Type: "string", Value: transfer.TxHash},
		}).Dispatch()
	}

	slog.Info("ingest deposit", "chain", chainID, "tx", transfer.TxHash, "log_index", transfer.LogIndex, "user", addr.ExternalUserID, "asset", asset, "amount", transfer.Amount.String())
	return nil
}

func (s *Service) publishDepositPending(ctx context.Context, tx models.Transaction) {
	if s.deposits == nil {
		slog.Error("deposit webhook not sent: no deposit events publisher configured", "event_type", types.EventDepositPending, "transaction_id", tx.ID)
		return
	}
	if err := s.deposits.Publish(ctx, types.EventDepositPending, tx); err != nil {
		slog.Error("deposit webhook not sent", "event_type", types.EventDepositPending, "transaction_id", tx.ID, "error", err)
	}
}
