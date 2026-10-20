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
	"github.com/macrowallets/waas/app/services/depositrecord"
	"github.com/macrowallets/waas/app/services/ingest/providers"
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
	depositrecord.Store
	CountInternalTransfers(ctx context.Context, chainID, txHash string, walletID uuid.UUID) (int64, error)
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
	pushes      PushRecorder
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
}

// NewService wires the ingest service from Deps.
func NewService(deps Deps) *Service {
	return &Service{
		addresses:   deps.Addresses,
		registry:    deps.Registry,
		webhookSvc:  deps.Webhook,
		addressRepo: deps.AddressRepo,
		txRepo:      deps.Transactions,
	}
}

// InboundEvent is one provider webhook after its signature has been checked
// and its payload parsed. The service never takes the raw provider JSON.
type InboundEvent struct {
	ChainID   string
	Transfers []providers.InboundTransfer
}

// Ingest records a verified webhook from the typed event, then notes when the
// chain's provider last pushed (push.go).
func (s *Service) Ingest(ctx context.Context, event InboundEvent) error {
	if err := s.ProcessTransfers(ctx, event.ChainID, event.Transfers); err != nil {
		return err
	}
	s.recordPush(ctx, event.ChainID)
	return nil
}

func (s *Service) ProcessTransfers(ctx context.Context, chainID string, transfers []providers.InboundTransfer) error {
	adapter, err := s.registry.Chain(chainID)
	if err != nil {
		if errors.Is(err, chainregistry.ErrUnknownChain) {
			return err
		}
		return fmt.Errorf("load chain %s: %w", chainID, err)
	}

	// The deposit rows are built first, so each one is recorded knowing the other
	// transfers of its transaction in this push (depositrecord.Record). A transfer the
	// database could not look up or record fails the push after the others are
	// recorded, so the provider delivers it again; a transfer that cannot be read is
	// only logged, as a retry would not change it.
	var rows []*models.Transaction
	var storeFailures []error
	siblings := make(map[string][]depositrecord.Transfer)
	for _, transfer := range transfers {
		row, err := s.depositRow(ctx, chainID, adapter, transfer)
		if err != nil {
			slog.Error("ingest process transfer", "tx", transfer.TxHash, "error", err)
			if errors.Is(err, ErrStoreFailed) {
				storeFailures = append(storeFailures, err)
			}
			continue
		}
		if row != nil {
			rows = append(rows, row)
			siblings[row.TxHash] = append(siblings[row.TxHash], depositrecord.TransferOf(*row))
		}
	}
	for _, row := range rows {
		if err := s.record(ctx, row, siblings[row.TxHash]); err != nil {
			slog.Error("ingest process transfer", "tx", row.TxHash, "error", err)
			storeFailures = append(storeFailures, err)
		}
	}
	return errors.Join(storeFailures...)
}

// ErrStoreFailed is a pushed transfer the database could not look up or record; the
// push fails, so the provider delivers it again (recording is idempotent).
var ErrStoreFailed = errors.New("ingest: the deposit store failed")

// record records one deposit row and publishes deposit.pending when it is new.
func (s *Service) record(ctx context.Context, tx *models.Transaction, siblings []depositrecord.Transfer) error {
	outcome, err := depositrecord.Record(ctx, s.txRepo, tx, siblings)
	if err != nil {
		return fmt.Errorf("%w: record deposit: %w", ErrStoreFailed, err)
	}
	if outcome != depositrecord.Created {
		depositrecord.LogSkipped(outcome, tx)
		return nil
	}
	s.publishDepositPending(ctx, *tx)
	slog.Info("ingest deposit", "chain", tx.Chain, "tx", tx.TxHash, "log_index", tx.LogIndex, "user", tx.ExternalUserID, "asset", tx.Asset, "amount", tx.Amount)
	return nil
}

// depositRow is the deposit row of a pushed transfer to a watched address, with its
// amount in base units and its token from the registry; nil when the transfer is not
// a deposit (an unwatched address, an unregistered token, a sweep of the wallet).
func (s *Service) depositRow(ctx context.Context, chainID string, adapter types.Chain, transfer providers.InboundTransfer) (*models.Transaction, error) {
	if transfer.Amount == nil && !transfer.AmountIsHuman {
		return nil, fmt.Errorf("missing amount")
	}

	if !s.watched(ctx, chainID, transfer.To) {
		return nil, nil
	}

	addr, err := s.addressRepo.FindByChainAndAddress(ctx, chainID, transfer.To)
	if errors.Is(err, models.ErrRepositoryNotFound) || (err == nil && addr == nil) {
		slog.Warn("ingest: watched-address set lists an address the database does not hold; transfer skipped", "chain", chainID, "tx", transfer.TxHash)
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%w: lookup address: %w", ErrStoreFailed, err)
	}

	internal, err := s.txRepo.CountInternalTransfers(ctx, chainID, transfer.TxHash, addr.WalletID)
	if err != nil {
		return nil, fmt.Errorf("%w: check internal transfer: %w", ErrStoreFailed, err)
	}
	if internal > 0 {
		slog.Info("ingest skipped a sweep or gas seed of the wallet", "chain", chainID, "tx", transfer.TxHash, "to", transfer.To)
		return nil, nil
	}

	asset := adapter.NativeAsset()
	var tokenContract string
	if transfer.Token != nil {
		seeded, registered := s.registeredToken(chainID, transfer.Token.Contract)
		if !registered {
			return nil, nil
		}
		asset = seeded.Symbol
		tokenContract = seeded.Contract
		transfer.Token.Symbol = seeded.Symbol
		transfer.Token.Decimals = seeded.Decimals
		if transfer.AmountIsHuman {
			base, convErr := withdraw.ResolveWithdrawalAmount(chainID, adapter.NativeAsset(), 0, seeded.Symbol, transfer.HumanAmount, []types.Token{*seeded})
			if convErr != nil {
				return nil, convErr
			}
			transfer.Amount = base.BaseUnits
		}
	} else if transfer.AmountIsHuman {
		reader, ok := adapter.(interface{ NativeDecimals() int })
		if !ok {
			return nil, fmt.Errorf("chain %s native decimals are not loaded", chainID)
		}
		base, convErr := amount.DecimalToBaseUnits(transfer.HumanAmount, reader.NativeDecimals())
		if convErr != nil {
			return nil, convErr
		}
		transfer.Amount = base
		if transfer.Asset != "" {
			asset = types.CanonicalAssetSymbol(transfer.Asset)
		}
	} else if transfer.Asset != "" {
		asset = types.CanonicalAssetSymbol(transfer.Asset)
	}
	if transfer.Amount == nil {
		return nil, fmt.Errorf("missing amount")
	}
	if transfer.Amount.Sign() < 0 {
		return nil, fmt.Errorf("amount %s: %w", transfer.Amount, amount.ErrNegativeAmount)
	}

	return &models.Transaction{
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
		Finality:       chain.FinalityPolicyOf(adapter),
		Status:         string(types.TxStatusPending),
		BlockNumber:    int64(transfer.BlockNumber),
		BlockHash:      transfer.BlockHash,
		LogIndex:       transfer.LogIndex,
		Direction:      models.TxDirectionInbound,
		Source:         models.TxSourceDepositIngest,
		RawPayload:     "{}",
	}, nil
}

// watched reports whether address is a watched address of the chain. A lookup that
// fails reports false, logged: the push does not record the transfer, the scanner
// does.
func (s *Service) watched(ctx context.Context, chainID, address string) bool {
	if s.addresses != nil {
		isMine, err := s.addresses.SIsMember(ctx, "vault:addresses:"+chainID, address).Result()
		if err != nil {
			slog.Warn("ingest: watched-address set unavailable, transfer left to the scanner", "chain", chainID, "error", err)
		}
		return err == nil && isMine
	}
	count, err := s.addressRepo.CountByChainAndAddress(ctx, chainID, address)
	if err != nil {
		slog.Warn("ingest: watched-address lookup failed, transfer left to the scanner", "chain", chainID, "error", err)
	}
	return err == nil && count > 0
}

// registeredToken is the chain's token row for contract; false for a token the
// registry does not list, which is not a deposit.
func (s *Service) registeredToken(chainID, contract string) (*types.Token, bool) {
	token, err := s.registry.FindTokenByContract(chainID, contract)
	return token, err == nil && token != nil
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
