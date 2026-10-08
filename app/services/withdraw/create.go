package withdraw

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/app/services/chainregistry"
	mpcpkg "github.com/macrowallets/waas/app/services/mpc"
	"github.com/macrowallets/waas/pkg/mpcshare"
	"github.com/macrowallets/waas/pkg/types"
)

// CreateStatus selects the response the withdrawal handler already used for a
// refusal that happens before the withdrawal row is marked failed.
type CreateStatus int

const (
	CreateStatusUnauthorized CreateStatus = iota + 1
	CreateStatusForbidden
	CreateStatusBadRequest
	CreateStatusUnprocessable
	CreateStatusInternal
	CreateStatusTooManyRequests
)

// CreateRefusal is a client refusal. Message is the legacy {"error": Message}
// text. The cause is not returned to the client.
type CreateRefusal struct {
	Status  CreateStatus
	Message string
	cause   error
}

func (e *CreateRefusal) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *CreateRefusal) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// CreateRowError is a withdrawal-row read or write that the handler logs and
// answers as a generic 500. Endpoint is the existing log label.
type CreateRowError struct {
	Endpoint string
	Err      error
}

func (e *CreateRowError) Error() string {
	if e == nil || e.Err == nil {
		return "create withdrawal"
	}
	return e.Err.Error()
}

func (e *CreateRowError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// UserLookup loads the dashboard user whose TOTP must authorize a withdrawal.
type UserLookup interface {
	FindByID(ctx context.Context, id uuid.UUID) (*models.User, error)
}

// TotpCheck spends the same TOTP step login uses. The code is not logged.
type TotpCheck interface {
	Verify(user *models.User, code, recoveryCode string) error
}

// WithdrawalRows is the idempotent withdrawal row. Signing stays in Request.
// Within opens one transaction for a new row and a separate one for a retry.
type WithdrawalRows interface {
	Within(ctx context.Context, fn func(context.Context) error) error
	FindByIDAndWallet(ctx context.Context, withdrawalID, walletID uuid.UUID) (*models.Withdrawal, error)
	Create(ctx context.Context, withdrawal *models.Withdrawal) error
	RetryBroadcast(ctx context.Context, id uuid.UUID, amount, destination, feeEstimate, note string) error
}

// ChainCatalog reads the chain row whose native decimals size the amount.
type ChainCatalog interface {
	FindByID(ctx context.Context, id string) (*models.Chain, error)
}

// CreateInput is the validated withdrawal body plus the caller the handler
// already authenticated. A nil DashboardUserID is an access-token caller:
// the token is the factor, so TOTP is not checked.
type CreateInput struct {
	Wallet             *models.Wallet
	DashboardUserID    uuid.UUID
	TotpCode           string
	Passphrase         string
	Asset              string
	Amount             string
	DestinationAddress string
	Note               string
	IdempotencyKey     string
}

// CreateResult is the withdrawal row after the idempotent write. Replayed
// means the row was already broadcast or confirmed, so the handler returns
// it and does not call Request. Resolved is the amount Request signs.
type CreateResult struct {
	Withdrawal     *models.Withdrawal
	Replayed       bool
	IdempotencyKey string
	Resolved       *ResolvedWithdrawal
}

// UseCreate attaches the readers Create needs. Request keeps its own gate,
// lock, and spending limit.
func (s *Service) UseCreate(users UserLookup, totp TotpCheck, rows WithdrawalRows, chains ChainCatalog) {
	if s == nil {
		return
	}
	s.createUsers = users
	s.createTotp = totp
	s.createRows = rows
	s.createChains = chains
}

// Create checks TOTP (dashboard callers), the wallet passphrase, the fee
// estimate, and the idempotent withdrawal row. It does not broadcast.
// withdrawals-enabled stays on the handler, before this method, and again
// inside Request before the Redis lock.
func (s *Service) Create(ctx context.Context, in CreateInput) (*CreateResult, error) {
	defer mpcshare.DiscardPassphrase(&in.Passphrase)
	if s == nil {
		return nil, fmt.Errorf("create withdrawal: service is required")
	}
	if ctx == nil {
		return nil, fmt.Errorf("create withdrawal: context is required")
	}
	if in.Wallet == nil {
		return nil, fmt.Errorf("create withdrawal: wallet is required")
	}
	if err := s.verifyDashboardTOTP(ctx, in.DashboardUserID, in.TotpCode); err != nil {
		return nil, err
	}
	if err := s.verifyPassphraseBeforePersist(ctx, in.Wallet, in.Passphrase); err != nil {
		return nil, err
	}
	resolved, adapter, err := s.resolveCreateAmount(ctx, in)
	if err != nil {
		return nil, err
	}
	withdrawalID, err := WithdrawalIDFromIdempotencyKey(in.IdempotencyKey)
	if err != nil {
		return nil, &CreateRefusal{Status: CreateStatusBadRequest, Message: err.Error()}
	}
	idempotencyKey := in.IdempotencyKey
	if idempotencyKey == "" {
		idempotencyKey = withdrawalID.String()
	}
	if in.Wallet.DepositAddress == nil {
		return nil, &CreateRefusal{Status: CreateStatusUnprocessable, Message: "wallet has no deposit address"}
	}
	feeEstimate := estimateCreateFee(ctx, adapter, in.Wallet.DepositAddress.Address, in.DestinationAddress, resolved)
	return s.persistCreate(ctx, in, withdrawalID, idempotencyKey, feeEstimate, resolved)
}

func (s *Service) verifyDashboardTOTP(ctx context.Context, userID uuid.UUID, code string) error {
	if userID == uuid.Nil {
		return nil
	}
	if s.createUsers == nil {
		return fmt.Errorf("create withdrawal: user lookup is required")
	}
	user, err := s.createUsers.FindByID(ctx, userID)
	if err != nil || user == nil {
		return &CreateRefusal{Status: CreateStatusUnauthorized, Message: "user not found"}
	}
	if !user.TotpEnabled {
		return &CreateRefusal{Status: CreateStatusForbidden, Message: "2FA must be enabled before withdrawing"}
	}
	if s.createTotp == nil {
		return &CreateRefusal{Status: CreateStatusInternal, Message: "internal error"}
	}
	err = s.createTotp.Verify(user, code, "")
	if err == nil {
		return nil
	}
	if errors.Is(err, authsvc.ErrInvalidSecondFactor) {
		return &CreateRefusal{Status: CreateStatusUnauthorized, Message: "invalid 2FA code"}
	}
	slog.Error(fmt.Sprintf("withdraw: totp: %v", err))
	return &CreateRefusal{Status: CreateStatusInternal, Message: "internal error"}
}

func (s *Service) verifyPassphraseBeforePersist(ctx context.Context, wallet *models.Wallet, passphrase string) error {
	defer mpcshare.DiscardPassphrase(&passphrase)
	if s.locker == nil {
		slog.Error("withdraw: passphrase attempt limiter needs Redis, which is not configured")
		return &CreateRefusal{Status: CreateStatusInternal, Message: "internal error"}
	}
	if err := s.checkRateLimit(ctx, wallet.ID.String()); err != nil {
		if errors.Is(err, ErrTooManyAttempts) {
			return &CreateRefusal{Status: CreateStatusTooManyRequests, Message: err.Error()}
		}
		slog.Error("withdraw: passphrase attempt limiter", "wallet_id", wallet.ID, "error", err)
		return &CreateRefusal{Status: CreateStatusInternal, Message: "internal error"}
	}
	shareA, err := wallet.DecryptShareA(passphrase)
	if err != nil {
		if errors.Is(err, mpcpkg.ErrInvalidPassphrase) {
			s.recordFailedAttempt(ctx, wallet.ID.String())
			return &CreateRefusal{Status: CreateStatusUnauthorized, Message: ErrInvalidPassphrase.Error()}
		}
		return &CreateRefusal{Status: CreateStatusInternal, Message: "internal error"}
	}
	zeroShare(shareA)
	return nil
}

func (s *Service) resolveCreateAmount(ctx context.Context, in CreateInput) (*ResolvedWithdrawal, types.Chain, error) {
	if s.registry == nil {
		return nil, nil, fmt.Errorf("create withdrawal: chain registry is required")
	}
	adapter, err := s.registry.Chain(in.Wallet.Chain)
	if err != nil {
		if errors.Is(err, chainregistry.ErrUnknownChain) {
			return nil, nil, &CreateRefusal{Status: CreateStatusUnprocessable, Message: err.Error(), cause: err}
		}
		return nil, nil, err
	}
	if s.createChains == nil {
		return nil, nil, fmt.Errorf("create withdrawal: chain catalog is required")
	}
	chainEntity, chainErr := s.createChains.FindByID(ctx, in.Wallet.Chain)
	if chainErr != nil || chainEntity == nil {
		return nil, nil, &CreateRefusal{Status: CreateStatusUnprocessable, Message: "chain not found"}
	}
	resolved, resolveErr := ResolveWithdrawalAmount(
		in.Wallet.Chain,
		adapter.NativeAsset(),
		chainEntity.NativeDecimals,
		in.Asset,
		in.Amount,
		s.registry.TokensForChain(in.Wallet.Chain),
	)
	if resolveErr != nil {
		return nil, nil, &CreateRefusal{Status: CreateStatusUnprocessable, Message: resolveErr.Error()}
	}
	if resolved == nil || resolved.BaseUnits == nil {
		return nil, nil, fmt.Errorf("create withdrawal: amount was not resolved")
	}
	return resolved, adapter, nil
}

func estimateCreateFee(ctx context.Context, adapter types.Chain, from, to string, resolved *ResolvedWithdrawal) string {
	feeEstimate := "0"
	if adapter == nil || resolved == nil {
		return feeEstimate
	}
	feeReq := types.TransferRequest{
		From:   from,
		To:     to,
		Amount: resolved.BaseUnits,
		Asset:  resolved.WalletAsset,
	}
	if resolved.Token != nil {
		feeReq.Token = resolved.Token
		feeReq.Asset = resolved.WalletAsset
	}
	estimate, estimateErr := adapter.EstimateFee(ctx, feeReq)
	if estimateErr == nil && estimate != nil && estimate.Fee != "" {
		feeEstimate = estimate.Fee
	}
	return feeEstimate
}

func (s *Service) persistCreate(ctx context.Context, in CreateInput, withdrawalID uuid.UUID, idempotencyKey, feeEstimate string, resolved *ResolvedWithdrawal) (*CreateResult, error) {
	if s.createRows == nil {
		return nil, fmt.Errorf("create withdrawal: withdrawal rows are required")
	}
	existing, findErr := s.createRows.FindByIDAndWallet(ctx, withdrawalID, in.Wallet.ID)
	if findErr != nil && !errors.Is(findErr, models.ErrRepositoryNotFound) {
		return nil, &CreateRowError{Endpoint: "find_idempotent_withdrawal", Err: findErr}
	}
	if existing != nil && (existing.Status == models.WithdrawalStatusBroadcast || existing.Status == models.WithdrawalStatusConfirmed) {
		return &CreateResult{
			Withdrawal:     existing,
			Replayed:       true,
			IdempotencyKey: idempotencyKey,
			Resolved:       resolved,
		}, nil
	}
	w := existing
	if w == nil {
		w = &models.Withdrawal{
			ID:                 withdrawalID,
			WalletID:           in.Wallet.ID,
			Status:             models.WithdrawalStatusBroadcasting,
			Amount:             in.Amount,
			DestinationAddress: in.DestinationAddress,
			FeeEstimate:        feeEstimate,
			Note:               in.Note,
		}
		if in.DashboardUserID != uuid.Nil {
			createdBy := in.DashboardUserID
			w.CreatedBy = &createdBy
		}
		if in.Wallet.AccountID != nil {
			w.AccountID = in.Wallet.AccountID
		}
		if createErr := s.createRows.Within(ctx, func(txCtx context.Context) error {
			return s.createRows.Create(txCtx, w)
		}); createErr != nil {
			return nil, &CreateRowError{Endpoint: "create_broadcasting_withdrawal", Err: createErr}
		}
	} else if updateErr := s.createRows.Within(ctx, func(txCtx context.Context) error {
		return s.createRows.RetryBroadcast(txCtx, w.ID, in.Amount, in.DestinationAddress, feeEstimate, in.Note)
	}); updateErr != nil {
		return nil, &CreateRowError{Endpoint: "retry_broadcasting_withdrawal", Err: updateErr}
	} else {
		w.Status = models.WithdrawalStatusBroadcasting
	}
	return &CreateResult{
		Withdrawal:     w,
		IdempotencyKey: idempotencyKey,
		Resolved:       resolved,
	}, nil
}

// WithdrawalIDFromIdempotencyKey uses the key as the withdrawal id when it is
// a UUID. An empty key gets a new id. Any other value is refused.
func WithdrawalIDFromIdempotencyKey(idempotencyKey string) (uuid.UUID, error) {
	if strings.TrimSpace(idempotencyKey) == "" {
		return uuid.New(), nil
	}
	id, err := uuid.Parse(idempotencyKey)
	if err != nil {
		return uuid.Nil, fmt.Errorf("idempotency_key must be a UUID")
	}
	return id, nil
}
