package withdrawals

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"
	"github.com/redis/go-redis/v9"

	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/middleware"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/policies"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	chain "github.com/macrowallets/waas/app/services/chain"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
	"github.com/macrowallets/waas/app/services/features"
	usersvc "github.com/macrowallets/waas/app/services/users"
	"github.com/macrowallets/waas/app/services/walletrecords"
	"github.com/macrowallets/waas/app/services/withdraw"
	"github.com/macrowallets/waas/app/services/withdrawalevents"
	"github.com/macrowallets/waas/app/services/withdrawalrecords"
	"github.com/macrowallets/waas/pkg/types"
)

func validateRequest(ctx http.Context, req http.FormRequest) http.Response {
	{
		return controllers.ValidateRequest(ctx, req)
	}
}

// WithdrawalsController serves the dashboard withdrawal routes.
type WithdrawalsController struct {
	withdrawals       *withdrawalrecords.Records
	chains            *chainsvc.Service
	users             *usersvc.Service
	registry          *chain.Registry
	withdrawalService *withdraw.Service
	passwords         *authsvc.Service
	flags             *features.Service
	events            *withdrawalevents.Publisher
	redis             *redis.Client
	memberships       *walletrecords.Memberships
	wallets           *walletrecords.Wallets
}

func NewWithdrawalsController(
	withdrawals *withdrawalrecords.Records,
	chains *chainsvc.Service,
	users *usersvc.Service,
	registry *chain.Registry,
	withdrawalService *withdraw.Service,
	passwords *authsvc.Service,
	flags *features.Service,
	events *withdrawalevents.Publisher,
	redis *redis.Client,
	memberships *walletrecords.Memberships,
	wallets *walletrecords.Wallets,
) *WithdrawalsController {
	if withdrawals == nil {
		panic("dashboard withdrawals controller: withdrawals service is required")
	}
	if chains == nil {
		panic("dashboard withdrawals controller: chains service is required")
	}
	if users == nil {
		panic("dashboard withdrawals controller: users service is required")
	}
	if registry == nil {
		panic("dashboard withdrawals controller: chain registry is required")
	}
	if withdrawalService == nil {
		panic("dashboard withdrawals controller: withdrawal service is required")
	}
	if passwords == nil {
		panic("dashboard withdrawals controller: auth service is required")
	}
	if flags == nil {
		panic("dashboard withdrawals controller: feature flags are required")
	}
	if memberships == nil {
		panic("dashboard withdrawals controller: wallet memberships are required")
	}
	if wallets == nil {
		panic("dashboard withdrawals controller: wallets service is required")
	}
	return &WithdrawalsController{
		withdrawals:       withdrawals,
		chains:            chains,
		users:             users,
		registry:          registry,
		withdrawalService: withdrawalService,
		passwords:         passwords,
		flags:             flags,
		events:            events,
		redis:             redis,
		memberships:       memberships,
		wallets:           wallets,
	}
}

// ListWalletWithdrawals godoc
// @Summary      List withdrawals for a wallet
// @Description  Returns a paginated list of withdrawals for a specific wallet
// @Tags         Wallet Withdrawals
// @Security     BearerAuth
// @Produce      json
// @Param        walletId  path    string  true   "Wallet UUID"
// @Param        status    query   string  false  "Status filter"  Enums(pending,approved,rejected,broadcast,confirmed,failed)
// @Param        limit     query   int     false  "Max results (default 50)"
// @Param        offset    query   int     false  "Pagination offset"
// @Success      200  {object}  WithdrawalListResponse
// @Failure      403  {object}  ErrorResponse
// @Failure      404  {object}  ErrorResponse
// @Router       /wallets/{walletId}/withdrawals [get]
func (ctrl *WithdrawalsController) ListWalletWithdrawals(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)

	limit, offset := pagination.ParseParams(ctx, 50)
	var query requests.ListWithdrawalsRequest
	query.Load(ctx)
	status := query.Status
	withdrawals, total, err := ctrl.withdrawals.FindByWallet(ctx.Context(), wallet.ID, status, limit, offset)
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to fetch withdrawals"})
	}
	return responses.Send(ctx, http.StatusOK, pagination.Response(controllers.WithdrawalViews(withdrawals), total, limit, offset))
}

func (ctrl *WithdrawalsController) EstimateWithdrawalFee(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)

	var req requests.EstimateWithdrawalRequest
	if resp := validateRequest(ctx, &req); resp != nil {
		return resp
	}

	adapter, err := ctrl.registry.Chain(wallet.Chain)
	if err != nil {
		return responses.Send(ctx, http.StatusUnprocessableEntity, http.Json{
			"error": "fee estimation unavailable",
			"code":  "FEE_ESTIMATE_FAILED",
		})
	}

	estimate, err := adapter.EstimateFee(ctx.Context(), types.TransferRequest{
		From:  "",
		To:    req.DestinationAddress,
		Asset: adapter.NativeAsset(),
	})
	if err != nil {
		return responses.Send(ctx, http.StatusUnprocessableEntity, http.Json{
			"error": "fee estimation unavailable",
			"code":  "FEE_ESTIMATE_FAILED",
		})
	}

	return responses.Send(ctx, http.StatusOK, estimate)
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
// @Success      201  {object}  controllers.WithdrawalView
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
	wallet := requestctx.MustWallet(ctx)
	if resp := controllers.BlockFlag(ctx, ctrl.flags, controllers.AccountIDForWallet(ctx, wallet), features.FlagWithdrawalsEnabled, features.CodeWithdrawalsPaused, "create_wallet_withdrawal"); resp != nil {
		return resp
	}

	var req requests.CreateWalletWithdrawalRequest
	if resp := validateRequest(ctx, &req); resp != nil {
		return resp
	}

	callerUserID, hasUser := requestctx.UserID(ctx)
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
		accountID, hasAccount := requestctx.AccountID(ctx)
		if !hasAccount || accountID == uuid.Nil {
			return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "unauthenticated"})
		}
	}

	if errResp := controllers.VerifyWalletPassphrase(ctx, ctrl.redis, wallet, req.Passphrase); errResp != nil {
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

	callerAccountID, _ := requestctx.AccountID(ctx)
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
		return responses.Send(ctx, http.StatusOK, controllers.WithdrawalViewPtr(existing))
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

	withdrawal := withdraw.WithdrawRequest{
		WalletID:        wallet.ID,
		ToAddress:       req.DestinationAddress,
		Amount:          resolved.BaseUnits.String(),
		Asset:           resolved.WalletAsset,
		Passphrase:      req.Passphrase,
		IdempotencyKey:  idempotencyKey,
		CallerAccountID: callerAccountID,
	}
	if token, ok := requestctx.APIToken(ctx); ok {
		withdrawal = withdrawal.WithAPIToken(token, req.Amount)
	}
	tx, _, err := ctrl.withdrawalService.Request(ctx.Context(), withdrawal)
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
		controllers.PublishWithdrawalFailed(ctx, ctrl.events, w, failureCode, withdrawalevents.FailedAttempt{
			Chain:     wallet.Chain,
			Asset:     resolved.WalletAsset,
			BaseUnits: resolved.BaseUnits,
		})
		if resp := controllers.MapSpendingLimitError(ctx, err); resp != nil {
			return resp
		}
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
	controllers.PublishWithdrawalBroadcast(ctx, ctrl.events, w, tx)
	return responses.Send(ctx, http.StatusCreated, controllers.WithdrawalViewPtr(w))
}

// GetWalletWithdrawal godoc
// @Summary      Get a single wallet withdrawal
// @Description  Returns a specific withdrawal by ID scoped to the wallet
// @Tags         Wallet Withdrawals
// @Security     BearerAuth
// @Produce      json
// @Param        walletId      path  string  true  "Wallet UUID"
// @Param        withdrawalId  path  string  true  "Withdrawal UUID"
// @Success      200  {object}  controllers.WithdrawalView
// @Failure      403  {object}  ErrorResponse
// @Failure      404  {object}  ErrorResponse
// @Router       /wallets/{walletId}/withdrawals/{withdrawalId} [get]
func (ctrl *WithdrawalsController) GetWalletWithdrawal(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)

	withdrawalID, err := requests.RouteUUID(ctx, "withdrawalId")
	if err != nil {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "invalid withdrawal id"})
	}

	w, err := ctrl.withdrawals.FindByIDAndWallet(ctx.Context(), withdrawalID, wallet.ID)
	if err != nil || w == nil {
		return responses.Send(ctx, http.StatusNotFound, http.Json{"error": "withdrawal not found"})
	}
	return responses.Send(ctx, http.StatusOK, controllers.WithdrawalViewPtr(w))
}

// GetDashboardWithdrawal godoc
// @Summary      Get a withdrawal by id
// @Description  Returns one withdrawal for the account in X-Account-Id. A missing id, or a withdrawal outside that account, is not found.
// @Tags         Wallet Withdrawals
// @Security     BearerAuth
// @Produce      json
// @Param        withdrawalId  path  string  true  "Withdrawal UUID"
// @Param        X-Account-Id  header  string  true  "Account UUID"
// @Success      200  {object}  controllers.WithdrawalView
// @Failure      401  {object}  ErrorResponse
// @Failure      404  {object}  ErrorResponse
// @Router       /withdrawals/{withdrawalId} [get]
func (ctrl *WithdrawalsController) GetDashboardWithdrawal(ctx http.Context) http.Response {
	accountID, ok := requestctx.AccountID(ctx)
	if !ok || accountID == uuid.Nil {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "X-Account-Id header is required"})
	}

	withdrawalID, err := requests.RouteUUID(ctx, "withdrawalId")
	if err != nil {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "invalid withdrawal id"})
	}

	withdrawal, err := ctrl.withdrawals.FindByID(ctx.Context(), withdrawalID)
	if err != nil || withdrawal == nil {
		return responses.Send(ctx, http.StatusNotFound, http.Json{"error": "withdrawal not found"})
	}

	wallet, err := ctrl.wallets.FindByID(ctx.Context(), withdrawal.WalletID)
	if err != nil || wallet == nil || wallet.AccountID == nil || *wallet.AccountID != accountID {
		return responses.Send(ctx, http.StatusNotFound, http.Json{"error": "withdrawal not found"})
	}
	return responses.Send(ctx, http.StatusOK, controllers.WithdrawalViewPtr(withdrawal))
}

// CancelWalletWithdrawal godoc
// @Summary      Cancel a pending withdrawal
// @Description  Cancels a withdrawal that is still in 'pending' status. Requires the creator or an owner/admin.
// @Tags         Wallet Withdrawals
// @Security     BearerAuth
// @Produce      json
// @Param        walletId      path  string  true  "Wallet UUID"
// @Param        withdrawalId  path  string  true  "Withdrawal UUID"
// @Success      200  {object}  controllers.WithdrawalView
// @Failure      403  {object}  ErrorResponse
// @Failure      404  {object}  ErrorResponse
// @Failure      422  {object}  ErrorResponse  "Withdrawal cannot be cancelled in current state"
// @Router       /wallets/{walletId}/withdrawals/{withdrawalId}/cancel [post]
func (ctrl *WithdrawalsController) CancelWalletWithdrawal(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)

	withdrawalID, err := requests.RouteUUID(ctx, "withdrawalId")
	if err != nil {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "invalid withdrawal id"})
	}

	w, err := ctrl.withdrawals.FindByIDAndWallet(ctx.Context(), withdrawalID, wallet.ID)
	if err != nil || w == nil {
		return responses.Send(ctx, http.StatusNotFound, http.Json{"error": "withdrawal not found"})
	}

	if w.Status != "pending" {
		return responses.Send(ctx, http.StatusUnprocessableEntity, http.Json{
			"error": "only pending withdrawals can be cancelled",
		})
	}

	creatorID := uuid.Nil
	if w.CreatedBy != nil {
		creatorID = *w.CreatedBy
	}
	if resp := controllers.Deny(ctx, policies.WalletCancelWithdrawal(controllers.WalletMembership(ctx, ctrl.memberships, wallet.ID), creatorID)); resp != nil {
		return resp
	}

	actorID := middleware.SessionUserID(ctx)
	if wallet.AccountID == nil || actorID == uuid.Nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to cancel withdrawal"})
	}
	if err := ctrl.withdrawals.Cancel(ctx.Context(), *wallet.AccountID, actorID, w.ID); err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to cancel withdrawal"})
	}
	w.Status = "cancelled"
	return responses.Send(ctx, http.StatusOK, controllers.WithdrawalViewPtr(w))
}
