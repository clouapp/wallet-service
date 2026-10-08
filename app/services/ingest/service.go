package ingest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/chainregistry"
	"github.com/macrowallets/waas/app/services/ingest/providers"
	"github.com/macrowallets/waas/app/services/refresh"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/app/services/withdraw"
	"github.com/macrowallets/waas/pkg/amount"
	"github.com/macrowallets/waas/pkg/types"
)

// addressReader is the address lookup ingest uses to attribute a transfer.
type addressReader interface {
	CountByChainAndAddress(ctx context.Context, chainID, address string) (int64, error)
	FindByChainAndAddress(ctx context.Context, chainID, address string) (*models.Address, error)
}

// transactionStore is the deposit row ingest writes after it attributes a transfer.
type transactionStore interface {
	CountByChainTxHashAndLogIndex(ctx context.Context, chainID, txHash string, logIndex int, txType string) (int64, error)
	CountInternalTransfers(ctx context.Context, chainID, txHash string, walletID uuid.UUID) (int64, error)
	Create(ctx context.Context, tx *models.Transaction) error
}

// AddressSet is the watched-address set. The provider supplies it; this package
// never imports the Redis client. A nil AddressSet means Redis is not configured.
type AddressSet interface {
	SIsMember(ctx context.Context, key string, member any) Membership
}

// Membership is one SISMEMBER answer. Result is false with a nil error when the member is absent.
type Membership interface {
	Result() (bool, error)
}

type Service struct {
	addresses   AddressSet
	registry    *chain.Registry
	webhookSvc  *webhook.Service
	addressRepo addressReader
	txRepo      transactionStore
	deposits    DepositEvents
	dispatcher  refresh.Dispatcher
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

// Deps is everything the ingest service needs. A nil field means that
// dependency is absent.
type Deps struct {
	Addresses    AddressSet
	Registry     *chain.Registry
	Webhook      *webhook.Service
	AddressRepo  addressReader
	Transactions transactionStore
	Dispatcher   refresh.Dispatcher
}

// NewService wires the ingest service from Deps.
func NewService(deps Deps) *Service {
	return &Service{
		addresses:   deps.Addresses,
		registry:    deps.Registry,
		webhookSvc:  deps.Webhook,
		addressRepo: deps.AddressRepo,
		txRepo:      deps.Transactions,
		dispatcher:  deps.Dispatcher,
	}
}

// InboundEvent is one provider webhook after its signature has been checked
// and its payload parsed. The service never takes the raw provider JSON.
type InboundEvent struct {
	ChainID   string
	Transfers []providers.InboundTransfer
}

// Ingest records a verified webhook from the typed event.
func (s *Service) Ingest(ctx context.Context, event InboundEvent) error {
	return s.ProcessTransfers(ctx, event.ChainID, event.Transfers)
}

func (s *Service) ProcessTransfers(ctx context.Context, chainID string, transfers []providers.InboundTransfer) error {
	adapter, err := s.registry.Chain(chainID)
	if err != nil {
		if errors.Is(err, chainregistry.ErrUnknownChain) {
			return err
		}
		return fmt.Errorf("load chain %s: %w", chainID, err)
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

	if s.addresses != nil {
		isMine, err := s.addresses.SIsMember(ctx, "vault:addresses:"+chainID, transfer.To).Result()
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

	exists, err := s.txRepo.CountByChainTxHashAndLogIndex(ctx, chainID, transfer.TxHash, transfer.LogIndex, models.TxTypeDeposit)
	if err != nil {
		return err
	}
	if exists > 0 {
		return nil
	}

	internal, err := s.txRepo.CountInternalTransfers(ctx, chainID, transfer.TxHash, addr.WalletID)
	if err != nil {
		return fmt.Errorf("check internal transfer: %w", err)
	}
	if internal > 0 {
		slog.Info("ingest skipped a sweep or gas seed of the wallet", "chain", chainID, "tx", transfer.TxHash, "to", transfer.To)
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
	} else if transfer.AmountIsHuman {
		reader, ok := adapter.(interface{ NativeDecimals() int })
		if !ok {
			return fmt.Errorf("chain %s native decimals are not loaded", chainID)
		}
		base, convErr := amount.DecimalToBaseUnits(transfer.HumanAmount, reader.NativeDecimals())
		if convErr != nil {
			return convErr
		}
		transfer.Amount = base
		if transfer.Asset != "" {
			asset = types.CanonicalAssetSymbol(transfer.Asset)
		}
	} else if transfer.Asset != "" {
		asset = types.CanonicalAssetSymbol(transfer.Asset)
	}
	if transfer.Amount == nil {
		return fmt.Errorf("missing amount")
	}
	if transfer.Amount.Sign() < 0 {
		return fmt.Errorf("amount %s: %w", transfer.Amount, amount.ErrNegativeAmount)
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

	if err := s.txRepo.Create(ctx, tx); err != nil {
		return fmt.Errorf("insert tx: %w", err)
	}

	s.publishDepositPending(ctx, *tx)
	s.dispatchTransactionRefresh(tx.WalletID.String(), chainID)
	s.dispatchDepositDetected(tx.WalletID.String(), chainID, transfer.TxHash)

	slog.Info("ingest deposit", "chain", chainID, "tx", transfer.TxHash, "log_index", transfer.LogIndex, "user", addr.ExternalUserID, "asset", asset, "amount", transfer.Amount.String())
	return nil
}

func (s *Service) dispatchTransactionRefresh(walletID, chainID string) {
	if s.dispatcher == nil {
		return
	}
	_ = s.dispatcher.DispatchTransactions(walletID, chainID)
}

func (s *Service) dispatchDepositDetected(walletID, chainID, txHash string) {
	if s.dispatcher == nil {
		return
	}
	_ = s.dispatcher.DispatchDepositDetected(walletID, chainID, txHash)
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
