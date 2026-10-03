package withdrawals

import (
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	chain "github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/features"
	"github.com/macrowallets/waas/app/services/withdraw"
	"github.com/macrowallets/waas/app/services/withdrawalevents"
	"github.com/macrowallets/waas/pkg/types"
)

func validateRequest(ctx http.Context, req http.FormRequest) http.Response {
	{
		return controllers.ValidateRequest(ctx, req)
	}
}

// WithdrawalsController serves the external withdrawal routes.
type WithdrawalsController struct {
	withdrawals       *repositories.WithdrawalRepository
	chains            *repositories.ChainRepository
	users             *repositories.UserRepository
	transactions      *repositories.TransactionRepository
	registry          *chain.Registry
	withdrawalService *withdraw.Service
	passwords         *authsvc.Service
	flags             *features.Service
}

func NewWithdrawalsController(
	withdrawals *repositories.WithdrawalRepository,
	chains *repositories.ChainRepository,
	users *repositories.UserRepository,
	transactions *repositories.TransactionRepository,
	registry *chain.Registry,
	withdrawalService *withdraw.Service,
	passwords *authsvc.Service,
	flags *features.Service,
) *WithdrawalsController {
	if withdrawals == nil {
		panic("external withdrawals controller: withdrawals repository is required")
	}
	if chains == nil {
		panic("external withdrawals controller: chains repository is required")
	}
	if users == nil {
		panic("external withdrawals controller: users repository is required")
	}
	if transactions == nil {
		panic("external withdrawals controller: transactions repository is required")
	}
	if registry == nil {
		panic("external withdrawals controller: chain registry is required")
	}
	if withdrawalService == nil {
		panic("external withdrawals controller: withdrawal service is required")
	}
	if passwords == nil {
		panic("external withdrawals controller: auth service is required")
	}
	if flags == nil {
		panic("external withdrawals controller: feature flags are required")
	}
	return &WithdrawalsController{
		withdrawals:       withdrawals,
		chains:            chains,
		users:             users,
		transactions:      transactions,
		registry:          registry,
		withdrawalService: withdrawalService,
		passwords:         passwords,
		flags:             flags,
	}
}

// CreateWalletWithdrawal godoc
// @Summary      Create a withdrawal for a wallet
// @Description  Initiates a new withdrawal. The withdrawal is created in 'pending' status and queued for approval/processing.
// @Tags         Wallet Withdrawals
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        walletId  path      string                    true  "Wallet UUID"
// @Param        request   body      CreateWalletWithdrawalSwagger  true  "Withdrawal payload"
// @Success      201  {object}  models.Withdrawal
// @Failure      400  {object}  ErrorResponse
// @Failure      403  {object}  ErrorResponse
// @Router       /wallets/{walletId}/withdrawals [post]
// CreateWalletWithdrawal serves both auth surfaces:
//
//   - Dashboard (/v1/*): SessionAuth injects "user_id"; the caller is a
//     human operator, so we additionally require TOTP (2FA).
//   - External API (/api/v1/*): APITokenAuth injects "account_id" and
//     "api_token"; the access token itself is the authentication factor
//     (reinforced by HMAC signing when the token has require_signature=true),
//     so no additional TOTP is required. The withdrawal is attributed to the
//     token's account, with CreatedBy left nil for external-API callers.
//
// Wallet-passphrase verification runs in both flows — the encrypted MPC
// share A is the final gate before a withdrawal row is persisted.
func (ctrl *WithdrawalsController) CreateWalletWithdrawal(ctx http.Context) http.Response {
	wallet := ctx.Value("wallet").(*models.Wallet)
	if resp := controllers.BlockFlag(ctx, ctrl.flags, controllers.AccountIDForWallet(ctx, wallet), features.FlagWithdrawalsEnabled, features.CodeWithdrawalsPaused, "create_wallet_withdrawal"); resp != nil {
		return resp
	}

	var req requests.CreateWalletWithdrawalRequest
	if resp := validateRequest(ctx, &req); resp != nil {
		return resp
	}

	callerUserID, hasUser := ctx.Value("user_id").(uuid.UUID)
	isDashboardCaller := hasUser && callerUserID != uuid.Nil

	if isDashboardCaller {
		user, err := ctrl.users.FindByID(ctx.Context(), callerUserID)
		if err != nil || user == nil {
			return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "user not found"})
		}

		if !user.TotpEnabled {
			return responses.Send(ctx, http.StatusForbidden, http.Json{"error": "2FA must be enabled before withdrawing"})
		}

		decryptedSecret, err := facades.Crypt().DecryptString(user.TotpSecret)
		if err != nil {
			return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "internal error"})
		}
		authService := ctrl.passwords
		if !authService.VerifyTOTP(decryptedSecret, req.TotpCode) {
			return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "invalid 2FA code"})
		}
	} else {
		accountID, hasAccount := ctx.Value("account_id").(uuid.UUID)
		if !hasAccount || accountID == uuid.Nil {
			return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "unauthenticated"})
		}
	}

	if errResp := controllers.VerifyWalletPassphrase(ctx, wallet, req.Passphrase); errResp != nil {
		return errResp
	}

	adapter, err := ctrl.registry.Chain(wallet.Chain)
	if err != nil {
		return responses.Send(ctx, http.StatusUnprocessableEntity, http.Json{"error": err.Error()})
	}

	chainEntity, chainErr := ctrl.chains.FindByID(ctx.Context(), wallet.Chain)
	if chainErr != nil || chainEntity == nil {
		return responses.Send(ctx, http.StatusUnprocessableEntity, http.Json{"error": "chain not found"})
	}
	resolved, resolveErr := withdraw.ResolveWithdrawalAmount(
		wallet.Chain,
		adapter.NativeAsset(),
		chainEntity.NativeDecimals,
		req.Asset,
		req.Amount,
		ctrl.registry.TokensForChain(wallet.Chain),
	)
	if resolveErr != nil {
		return responses.Send(ctx, http.StatusUnprocessableEntity, http.Json{"error": resolveErr.Error()})
	}

	callerAccountID, _ := ctx.Value("account_id").(uuid.UUID)
	if callerAccountID == uuid.Nil && wallet.AccountID != nil {
		callerAccountID = *wallet.AccountID
	}

	withdrawalID, err := controllers.WithdrawalIDFromIdempotencyKey(req.IdempotencyKey)
	if err != nil {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": err.Error()})
	}
	idempotencyKey := req.IdempotencyKey
	if idempotencyKey == "" {
		idempotencyKey = withdrawalID.String()
	}
	if wallet.DepositAddress == nil {
		return responses.Send(ctx, http.StatusUnprocessableEntity, http.Json{
			"error": "wallet has no deposit address",
		})
	}
	feeEstimate := "0"
	feeReq := types.TransferRequest{
		From:   wallet.DepositAddress.Address,
		To:     req.DestinationAddress,
		Amount: resolved.BaseUnits,
		Asset:  resolved.WalletAsset,
	}
	if resolved.Token != nil {
		feeReq.Token = resolved.Token
		feeReq.Asset = resolved.WalletAsset
	}
	if estimate, estimateErr := adapter.EstimateFee(ctx.Context(), feeReq); estimateErr == nil && estimate != nil && estimate.Fee != "" {
		feeEstimate = estimate.Fee
	}

	existing, findErr := ctrl.withdrawals.FindByIDAndWallet(ctx.Context(), withdrawalID, wallet.ID)
	if findErr != nil && !errors.Is(findErr, models.ErrRepositoryNotFound) {
		return controllers.MapInternalError(ctx, findErr, "find_idempotent_withdrawal")
	}
	if existing != nil && (existing.Status == "broadcast" || existing.Status == "confirmed") {
		return ctx.Response().Json(http.StatusOK, existing)
	}

	w := existing
	if w == nil {
		w = &models.Withdrawal{
			ID:                 withdrawalID,
			WalletID:           wallet.ID,
			Status:             "broadcasting",
			Amount:             req.Amount,
			DestinationAddress: req.DestinationAddress,
			FeeEstimate:        feeEstimate,
			Note:               req.Note,
		}
		if isDashboardCaller {
			w.CreatedBy = &callerUserID
		}
		if wallet.AccountID != nil {
			w.AccountID = wallet.AccountID
		}
		if createErr := ctrl.withdrawals.Create(ctx.Context(), w); createErr != nil {
			return controllers.MapInternalError(ctx, createErr, "create_broadcasting_withdrawal")
		}
	} else {
		if updateErr := ctrl.withdrawals.RetryBroadcast(ctx.Context(), w.ID, req.Amount, req.DestinationAddress, feeEstimate, req.Note); updateErr != nil {
			return controllers.MapInternalError(ctx, updateErr, "retry_broadcasting_withdrawal")
		}
		w.Status = "broadcasting"
	}

	tx, _, err := ctrl.withdrawalService.Request(ctx.Context(), withdraw.WithdrawRequest{
		WalletID:        wallet.ID,
		ToAddress:       req.DestinationAddress,
		Amount:          resolved.BaseUnits.String(),
		Asset:           resolved.WalletAsset,
		Passphrase:      req.Passphrase,
		IdempotencyKey:  idempotencyKey,
		CallerAccountID: callerAccountID,
	})
	if err != nil {
		failureCode := controllers.WithdrawalFailureCode(err)
		if updateErr := ctrl.withdrawals.MarkFailed(ctx.Context(), w.ID, failureCode); updateErr != nil {
			return controllers.MapInternalError(
				ctx,
				fmt.Errorf("execute withdrawal: %v; mark failed: %w", err, updateErr),
				"mark_withdrawal_failed",
			)
		}
		w.Status = models.WithdrawalStatusFailed
		controllers.PublishWithdrawalFailed(ctx, w, failureCode, withdrawalevents.FailedAttempt{
			Chain:     wallet.Chain,
			Asset:     resolved.WalletAsset,
			BaseUnits: resolved.BaseUnits,
		})
		if resp := controllers.MapSweepError(ctx, err); resp != nil {
			return resp
		}
		switch {
		case errors.Is(err, withdraw.ErrInvalidPassphrase):
			return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": err.Error()})
		case errors.Is(err, withdraw.ErrPassphraseTooShort):
			return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": err.Error()})
		case errors.Is(err, withdraw.ErrInsufficientFunds):
			return responses.Send(ctx, http.StatusUnprocessableEntity, http.Json{"error": err.Error()})
		case errors.Is(err, withdraw.ErrConcurrentWithdraw):
			return responses.Send(ctx, http.StatusConflict, http.Json{"error": err.Error()})
		case errors.Is(err, withdraw.ErrTooManyAttempts):
			return responses.Send(ctx, http.StatusTooManyRequests, http.Json{"error": err.Error()})
		default:
			return controllers.MapInternalError(ctx, err, "create_wallet_withdrawal")
		}
	}

	w.Status = "broadcast"
	if tx != nil {
		w.TransactionID = &tx.ID
		w.TxHash = tx.TxHash
	}
	if updateErr := ctrl.withdrawals.MarkBroadcast(ctx.Context(), w.ID, w.TransactionID); updateErr != nil {
		return controllers.MapInternalError(ctx, updateErr, "persist_broadcast_withdrawal")
	}
	controllers.PublishWithdrawalBroadcast(ctx, w, tx)
	return ctx.Response().Json(http.StatusCreated, w)
}

// GetWalletWithdrawalByIdempotencyKey godoc
// @Summary      Look up a withdrawal by idempotency key
// @Description  Returns the outcome of a withdrawal created with the given idempotency_key (the key is also the withdrawal id). Lets a client that lost the create response learn whether the withdrawal was broadcast or failed. Scoped to the token's account.
// @Tags         Wallet Withdrawals
// @Security     BearerAuth
// @Produce      json
// @Param        walletId        path  string  true  "Wallet UUID"
// @Param        idempotencyKey  path  string  true  "Idempotency key (UUID) sent when the withdrawal was created"
// @Success      200  {object}  WithdrawalLookupResponse
// @Failure      400  {object}  ErrorResponse  "idempotency_key must be a UUID"
// @Failure      401  {object}  ErrorResponse
// @Failure      404  {object}  ErrorResponse  "wallet not found / withdrawal not found"
// @Router       /api/v1/wallets/{walletId}/withdrawals/{idempotencyKey} [get]
func (ctrl *WithdrawalsController) GetWalletWithdrawalByIdempotencyKey(ctx http.Context) http.Response {
	wallet := ctx.Value("wallet").(*models.Wallet)

	withdrawalID, err := uuid.Parse(strings.TrimSpace(ctx.Request().Route("idempotencyKey")))
	if err != nil {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "idempotency_key must be a UUID"})
	}

	w, err := ctrl.withdrawals.FindByIDAndWallet(ctx.Context(), withdrawalID, wallet.ID)
	if err != nil || w == nil {
		return responses.Send(ctx, http.StatusNotFound, http.Json{"error": "withdrawal not found"})
	}

	response := controllers.WithdrawalLookupResponse{
		ID:                 w.ID.String(),
		IdempotencyKey:     w.ID.String(),
		WalletID:           w.WalletID.String(),
		Status:             w.Status,
		Amount:             w.Amount,
		DestinationAddress: w.DestinationAddress,
		FailureReason:      w.FailureReason,
		CreatedAt:          w.CreatedAt,
		UpdatedAt:          w.UpdatedAt,
	}

	if w.TransactionID != nil {
		tx, txErr := ctrl.transactions.FindByID(ctx.Context(), *w.TransactionID)
		if txErr != nil {
			return controllers.MapInternalError(ctx, txErr, "lookup_withdrawal_transaction")
		}
		if tx != nil {
			if tx.TxHash != "" {
				response.TxHash = &tx.TxHash
			}
			response.TransactionStatus = &tx.Status
		}
	}

	return ctx.Response().Json(http.StatusOK, response)
}
