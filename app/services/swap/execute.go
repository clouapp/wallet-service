package swap

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/amount"
	"github.com/macrowallets/waas/pkg/mpcshare"
)

// Credentials opens the accepted provider's current key even while new quotes are disabled.
type Credentials interface {
	Provider(context.Context, string) (ProviderConfig, error)
}

// ExecuteInput carries the accepted quote and ephemeral authorization for one execution.
type ExecuteInput struct {
	QuoteID        uuid.UUID
	IdempotencyKey uuid.UUID
	UserID         uuid.UUID
	TOTP           string
	Passphrase     string
	APIToken       *models.AccessToken
	// Resume is set only by orchestration after checking actor and expiry.
	Resume bool
}

// SendResult identifies a funding transaction that was persisted before its broadcast.
type SendResult struct {
	Hash      string
	Uncertain bool
	Pending   bool
}

// Execution owns custody authorization and funding; provider adapters never receive these credentials.
type Execution interface {
	Authorize(context.Context, *models.Wallet, ExecuteInput) error
	Send(context.Context, *models.SwapOrder, *models.Wallet, *Quote, *Funding, ExecuteInput) (*SendResult, error)
}

// Verifier proves the amount credited to the exact receiving asset and address on chain.
type Verifier interface {
	Credit(context.Context, CreditRequest) (string, bool, error)
}

// CreditRequest associates one provider leg with an independently observed,
// exclusively allocated part of its receiving transaction.
type CreditRequest struct {
	OrderID     uuid.UUID
	WalletID    uuid.UUID
	Direction   string
	Asset       Asset
	Address     string
	Hash        string
	ReportedRaw string
}

// Execute claims an immutable quote once, then executes only its accepted provider.
func (s *Service) Execute(ctx context.Context, account *models.Account, source *models.Wallet, in ExecuteInput) (*models.SwapOrder, error) {
	defer mpcshare.DiscardPassphrase(&in.Passphrase)
	in.Resume = false
	if !ownsWallet(account, source) {
		return nil, ErrNotFound
	}
	if in.QuoteID == uuid.Nil || in.IdempotencyKey == uuid.Nil {
		return nil, ErrInvalidRequest
	}
	if in.UserID == uuid.Nil && in.APIToken == nil {
		return nil, ErrInvalidRequest
	}
	if s.deps.Execution == nil || s.deps.Policy == nil || s.deps.Credentials == nil {
		return nil, ErrDisabled
	}
	existing, err := s.deps.Store.FindOrderByKey(ctx, account.ID, in.IdempotencyKey)
	if err == nil {
		if existing.QuoteID != in.QuoteID || existing.SourceWalletID != source.ID {
			return nil, ErrConflict
		}
		if existing.Status != StatusPreparing {
			return existing, nil
		}
		if !sameActor(existing, in) || !existing.AuthorizedUntil.After(time.Now()) {
			return nil, ErrConflict
		}
		in.Resume = true
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	row, err := s.deps.Store.FindQuote(ctx, account.ID, source.ID, in.QuoteID)
	if err != nil {
		return nil, err
	}
	quote, err := DecodeQuote(row)
	if err != nil {
		return nil, err
	}
	if !quote.ExpiresAt.After(time.Now()) {
		if existing != nil {
			return s.failBeforeFunding(ctx, existing, "quote_expired", ErrExpired)
		}
		return nil, ErrExpired
	}
	if source.Status != "active" || account.Status != "active" || source.RequiredApprovals > 1 || (source.FrozenUntil != nil && source.FrozenUntil.After(time.Now())) {
		return nil, ErrWalletBlocked
	}
	destination, err := s.deps.Wallets.FindByID(ctx, row.DestinationWalletID)
	if err != nil {
		return nil, err
	}
	if !ownsWallet(account, destination) {
		return nil, ErrNotFound
	}
	if destination.Status != "active" {
		return nil, ErrWalletBlocked
	}
	if source.DepositAddress == nil || destination.DepositAddress == nil || source.DepositAddress.Address != quote.Request.FromAddress || destination.DepositAddress.Address != quote.Request.ToAddress {
		return nil, ErrExpired
	}
	from, err := s.asset(ctx, account, source, quote.Request.From.Symbol)
	if err != nil {
		return nil, err
	}
	to, err := s.asset(ctx, account, destination, quote.Request.To.Symbol)
	if err != nil {
		return nil, err
	}
	if !from.Same(quote.Request.From) || !to.Same(quote.Request.To) || from.Decimals != quote.Request.From.Decimals || to.Decimals != quote.Request.To.Decimals {
		return nil, ErrExpired
	}
	config, err := s.deps.Policy.Resolve(ctx, from.Symbol, from.Chain)
	if err != nil {
		return nil, err
	}
	if !config.Enabled || !config.Providers[quote.Provider].Enabled {
		return nil, ErrDisabled
	}
	if err := s.deps.Execution.Authorize(ctx, source, in); err != nil {
		return nil, err
	}
	order := existing
	if order == nil {
		order = &models.SwapOrder{ID: uuid.New(), AccountID: account.ID, QuoteID: quote.ID, IdempotencyKey: in.IdempotencyKey, SourceWalletID: source.ID, DestinationWalletID: destination.ID, Provider: quote.Provider, Status: StatusPreparing, FundingPayload: "{}", ReceivedRaw: "0", RefundRaw: "0", AuthorizedUntil: time.Now().Add(5 * time.Minute)}
		if in.UserID != uuid.Nil {
			order.AuthorizedUserID = &in.UserID
		}
		if in.APIToken != nil {
			order.AuthorizedTokenID = &in.APIToken.ID
		}
		if err := s.deps.Store.CreateOrder(ctx, order); err != nil {
			if errors.Is(err, ErrConflict) {
				replayed, readErr := s.deps.Store.FindOrderByKey(ctx, account.ID, in.IdempotencyKey)
				if readErr == nil && replayed.QuoteID == quote.ID && replayed.SourceWalletID == source.ID {
					return replayed, nil
				}
			}
			return nil, err
		}
	}
	return s.executeFunding(ctx, order, source, quote, in)
}

func sameActor(order *models.SwapOrder, in ExecuteInput) bool {
	if order.AuthorizedUserID != nil {
		return *order.AuthorizedUserID == in.UserID && in.APIToken == nil
	}
	return order.AuthorizedTokenID != nil && in.APIToken != nil && *order.AuthorizedTokenID == in.APIToken.ID && in.UserID == uuid.Nil
}

func (s *Service) executeFunding(ctx context.Context, order *models.SwapOrder, source *models.Wallet, quote *Quote, in ExecuteInput) (*models.SwapOrder, error) {
	adapter, err := s.deps.Factory.Get(quote.Provider)
	if err != nil {
		return s.failBeforeFunding(ctx, order, "provider_unavailable", err)
	}
	credential, err := s.deps.Credentials.Provider(ctx, quote.Provider)
	if err != nil {
		return s.failBeforeFunding(ctx, order, "provider_unavailable", err)
	}
	var funding *Funding
	if order.FundingPayload != "" && order.FundingPayload != "{}" {
		funding = &Funding{}
		if err := json.Unmarshal([]byte(order.FundingPayload), funding); err != nil {
			return nil, err
		}
	} else {
		funding, err = adapter.Build(ctx, quote, credential)
		if err != nil {
			return s.failBeforeFunding(ctx, order, quoteFailureCode(err), err)
		}
	}
	if !quote.ExpiresAt.After(time.Now()) {
		return s.failBeforeFunding(ctx, order, "quote_expired", ErrExpired)
	}
	payload, err := json.Marshal(funding)
	if err != nil {
		return s.failBeforeFunding(ctx, order, "invalid_funding", err)
	}
	if err := s.deps.Store.SaveFunding(ctx, order.ID, string(payload)); err != nil {
		return nil, err
	}
	order.FundingPayload = string(payload)
	result, sendErr := s.deps.Execution.Send(ctx, order, source, quote, funding, in)
	if result != nil && result.Pending {
		return order, nil
	}
	if result == nil || result.Hash == "" {
		if sendErr != nil && errors.Is(sendErr, ErrExecutionFailed) {
			code := "funding_failed"
			if errors.Is(sendErr, ErrExpired) {
				code = "quote_expired"
			}
			if errors.Is(sendErr, ErrUnsupported) {
				code = "unsupported"
			}
			return s.failBeforeFunding(ctx, order, code, sendErr)
		}
		// An executor error is not evidence that funds were never sent. Only an
		// explicit pre-send failure may release the order for a new quote.
		order.Status = StatusReconciling
		if err := s.deps.Store.RecordProgress(ctx, order, StatusPreparing); err != nil {
			return nil, err
		}
		return order, nil
	}
	order.SourceTxHash = result.Hash
	order.Status = StatusBroadcast
	if result.Uncertain || sendErr != nil {
		order.Status = StatusReconciling
	}
	if err := s.deps.Store.RecordSource(ctx, order.ID, result.Hash, order.Status); err != nil {
		return nil, err
	}
	return order, nil
}

func (s *Service) failBeforeFunding(ctx context.Context, order *models.SwapOrder, code string, cause error) (*models.SwapOrder, error) {
	order.Status = StatusFailed
	order.FailureCode = code
	if err := s.deps.Store.RecordProgress(ctx, order, StatusPreparing); err != nil {
		return nil, err
	}
	return order, nil
}

// Refresh tracks the accepted provider and requires independent confirmation before crediting.
func (s *Service) Refresh(ctx context.Context, account *models.Account, source *models.Wallet, id uuid.UUID) (*models.SwapOrder, error) {
	order, err := s.Find(ctx, account, source, id)
	if err != nil {
		return nil, err
	}
	if terminalStatus(order.Status) {
		return order, nil
	}
	row, err := s.deps.Store.FindQuote(ctx, account.ID, source.ID, order.QuoteID)
	if err != nil {
		return nil, err
	}
	quote, err := DecodeQuote(row)
	if err != nil {
		return nil, err
	}
	if order.SourceTxHash == "" {
		if order.Status == StatusPreparing && !quote.ExpiresAt.After(time.Now()) {
			order.Status, order.FailureCode = StatusExpired, "quote_expired"
			if err := s.deps.Store.RecordProgress(ctx, order, StatusPreparing); err != nil {
				return nil, err
			}
		}
		return order, nil
	}
	adapter, err := s.deps.Factory.Get(order.Provider)
	if err != nil {
		return nil, err
	}
	credential, err := s.deps.Credentials.Provider(ctx, order.Provider)
	if err != nil {
		return nil, err
	}
	progress, err := adapter.Status(ctx, quote, order.SourceTxHash, credential)
	if err != nil {
		return order, nil
	}
	previous := order.Status
	order.Status = progress.Status
	// A provider failure after funding does not prove a recoverable balance.
	if progress.Status == StatusFailed || progress.Status == StatusPartial {
		order.Status = StatusReconciling
	}
	if progress.Status == StatusCompleted {
		order.Status = StatusProcessing
		if s.deps.Verifier != nil && progress.ReceivingHash != "" && progress.ReceivingAsset.Same(quote.Request.To) {
			credited, confirmed, verifyErr := s.deps.Verifier.Credit(ctx, CreditRequest{OrderID: order.ID, WalletID: order.DestinationWalletID, Direction: "receive", Asset: quote.Request.To, Address: quote.Request.ToAddress, Hash: progress.ReceivingHash, ReportedRaw: progress.ReceivedRaw})
			if verifyErr == nil && confirmed {
				actual, ok := amount.ParseBaseUnits(credited)
				minimum, minOK := amount.ParseBaseUnits(quote.MinimumRaw)
				if ok && minOK && actual.Sign() > 0 {
					order.Status = StatusCompleted
					if actual.Cmp(minimum) < 0 {
						order.Status = StatusPartial
					}
					order.ReceivedRaw = actual.String()
					order.DestinationTxHash = progress.ReceivingHash
				}
			}
		}
	}
	if progress.Status == StatusRefunded {
		order.Status = StatusRefundPending
		if s.deps.Verifier != nil && progress.RefundHash != "" {
			credited, confirmed, verifyErr := s.deps.Verifier.Credit(ctx, CreditRequest{OrderID: order.ID, WalletID: source.ID, Direction: "refund", Asset: quote.Request.From, Address: quote.Request.FromAddress, Hash: progress.RefundHash, ReportedRaw: progress.RefundRaw})
			if verifyErr == nil && confirmed {
				actual, ok := amount.ParseBaseUnits(credited)
				if ok && actual.Sign() > 0 {
					order.Status = StatusRefunded
					order.RefundRaw = actual.String()
					order.RefundTxHash = progress.RefundHash
				}
			}
		}
	}
	now := time.Now().UTC()
	order.LastCheckedAt = &now
	if err := s.deps.Store.RecordProgress(ctx, order, previous); err != nil {
		return nil, err
	}
	return order, nil
}

func terminalStatus(status string) bool {
	return status == StatusCompleted || status == StatusRefunded || status == StatusFailed || status == StatusExpired || status == StatusPartial
}
