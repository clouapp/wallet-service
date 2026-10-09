package withdrawals

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	withdrawalresource "github.com/macrowallets/waas/app/http/resources/withdrawals"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	chain "github.com/macrowallets/waas/app/services/chain"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
	"github.com/macrowallets/waas/app/services/features"
	usersvc "github.com/macrowallets/waas/app/services/users"
	"github.com/macrowallets/waas/app/services/walletrecords"
	"github.com/macrowallets/waas/app/services/withdraw"
	"github.com/macrowallets/waas/app/services/withdrawalevents"
	"github.com/macrowallets/waas/app/services/withdrawalrecords"
)

// WithdrawalsController serves the external withdrawal routes.
type WithdrawalsController struct {
	withdrawals       *withdrawalrecords.Records
	chains            *chainsvc.Service
	users             *usersvc.Service
	transactions      *walletrecords.Transactions
	registry          *chain.Registry
	withdrawalService *withdraw.Service
	passwords         *authsvc.Service
	flags             *features.Service
	events            *withdrawalevents.Publisher
	secondFactor      *authsvc.SecondFactorVerifier
}

// WithdrawalsControllerDeps is everything the external withdrawals controller needs.
// Events may be nil.
type WithdrawalsControllerDeps struct {
	Withdrawals       *withdrawalrecords.Records
	Chains            *chainsvc.Service
	Users             *usersvc.Service
	Transactions      *walletrecords.Transactions
	Registry          *chain.Registry
	WithdrawalService *withdraw.Service
	Passwords         *authsvc.Service
	Flags             *features.Service
	Events            *withdrawalevents.Publisher
	SecondFactor      *authsvc.SecondFactorVerifier
}

// NewWithdrawalsController wires the external withdrawal handlers from WithdrawalsControllerDeps.
func NewWithdrawalsController(deps WithdrawalsControllerDeps) *WithdrawalsController {
	if deps.Withdrawals == nil {
		panic("external withdrawals controller: withdrawals service is required")
	}
	if deps.Chains == nil {
		panic("external withdrawals controller: chains service is required")
	}
	if deps.Users == nil {
		panic("external withdrawals controller: users service is required")
	}
	if deps.Transactions == nil {
		panic("external withdrawals controller: transactions service is required")
	}
	if deps.Registry == nil {
		panic("external withdrawals controller: chain registry is required")
	}
	if deps.WithdrawalService == nil {
		panic("external withdrawals controller: withdrawal service is required")
	}
	if deps.Passwords == nil {
		panic("external withdrawals controller: auth service is required")
	}
	if deps.Flags == nil {
		panic("external withdrawals controller: feature flags are required")
	}
	if deps.SecondFactor == nil {
		panic("external withdrawals controller: second factor verifier is required")
	}
	return &WithdrawalsController{
		withdrawals:       deps.Withdrawals,
		chains:            deps.Chains,
		users:             deps.Users,
		transactions:      deps.Transactions,
		registry:          deps.Registry,
		withdrawalService: deps.WithdrawalService,
		passwords:         deps.Passwords,
		flags:             deps.Flags,
		events:            deps.Events,
		secondFactor:      deps.SecondFactor,
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
// @Param        request   body      controllers.CreateWalletWithdrawalSwagger  true  "Withdrawal payload"
// @Success      201  {object}  withdrawalresource.Withdrawal
// @Failure      400  {object}  responses.ErrorBody
// @Failure      403  {object}  responses.ErrorBody
// @Failure      429  {object}  responses.ErrorBody  "Rate limit exceeded (too_many_requests, Retry-After header)"
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
	defer controllers.DiscardPassphrase(&req.Passphrase)
	if resp := requests.Validate(ctx, &req); resp != nil {
		return resp
	}

	callerUserID, hasUser := requestctx.UserID(ctx)
	dashboardUserID := uuid.Nil
	if hasUser && callerUserID != uuid.Nil {
		dashboardUserID = callerUserID
	} else {
		accountID, hasAccount := requestctx.AccountID(ctx)
		if !hasAccount || accountID == uuid.Nil {
			return responses.Fail(ctx, http.StatusUnauthorized, "unauthenticated", "unauthenticated")
		}
		// A set spending_limit is enforced later, inside withdraw.Service,
		// after the passphrase check. This handler does not reserve the cap.
	}

	callerAccountID, _ := requestctx.AccountID(ctx)
	if callerAccountID == uuid.Nil && wallet.AccountID != nil {
		callerAccountID = *wallet.AccountID
	}

	input := withdraw.CreateInput{
		Wallet:             wallet,
		DashboardUserID:    dashboardUserID,
		TotpCode:           req.TotpCode,
		Passphrase:         req.Passphrase,
		Asset:              req.Asset,
		Amount:             req.Amount,
		DestinationAddress: req.DestinationAddress,
		Note:               req.Note,
		IdempotencyKey:     req.IdempotencyKey,
	}
	created, err := ctrl.withdrawalService.Create(ctx.Context(), input)
	controllers.DiscardPassphrase(&input.Passphrase)
	if err != nil {
		return controllers.MapWithdrawalCreateError(ctx, err)
	}
	if created == nil || created.Withdrawal == nil || created.Resolved == nil || created.Resolved.BaseUnits == nil {
		return controllers.MapInternalError(ctx, fmt.Errorf("create withdrawal: empty result"), "create_wallet_withdrawal")
	}
	if created.Replayed {
		return ctx.Response().Success().Json(withdrawalresource.WithdrawalPtr(created.Withdrawal))
	}
	resolved := created.Resolved
	idempotencyKey := created.IdempotencyKey
	w := created.Withdrawal

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
	defer controllers.DiscardPassphrase(&withdrawal.Passphrase)
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
			return responses.Error(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, withdraw.ErrInvalidPassphrase.Error())
		case errors.Is(err, withdraw.ErrPassphraseTooShort):
			return responses.Error(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, withdraw.ErrPassphraseTooShort.Error())
		case errors.Is(err, withdraw.ErrInsufficientFunds):
			return responses.Error(ctx, http.StatusUnprocessableEntity, responses.CodeUnprocessable, withdraw.ErrInsufficientFunds.Error())
		case errors.Is(err, withdraw.ErrConcurrentWithdraw):
			return responses.Error(ctx, http.StatusConflict, responses.CodeConflict, withdraw.ErrConcurrentWithdraw.Error())
		case errors.Is(err, withdraw.ErrTooManyAttempts):
			return responses.Error(ctx, http.StatusTooManyRequests, responses.CodeTooManyRequests, withdraw.ErrTooManyAttempts.Error())
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
	return ctx.Response().Status(http.StatusCreated).Json(withdrawalresource.WithdrawalPtr(w))
}

// GetWalletWithdrawalByIdempotencyKey godoc
// @Summary      Look up a withdrawal by idempotency key
// @Description  Returns the outcome of a withdrawal created with the given idempotency_key (the key is also the withdrawal id). Lets a client that lost the create response learn whether the withdrawal was broadcast or failed. Scoped to the token's account.
// @Tags         Wallet Withdrawals
// @Security     BearerAuth
// @Produce      json
// @Param        walletId        path  string  true  "Wallet UUID"
// @Param        idempotencyKey  path  string  true  "Idempotency key (UUID) sent when the withdrawal was created"
// @Success      200  {object}  controllers.WithdrawalLookupResponse
// @Failure      400  {object}  responses.ErrorBody  "idempotency_key must be a UUID"
// @Failure      401  {object}  responses.ErrorBody
// @Failure      404  {object}  responses.ErrorBody  "wallet not found / withdrawal not found"
// @Failure      429  {object}  responses.ErrorBody  "Rate limit exceeded (too_many_requests, Retry-After header)"
// @Router       /api/v1/wallets/{walletId}/withdrawals/{idempotencyKey} [get]
func (ctrl *WithdrawalsController) GetWalletWithdrawalByIdempotencyKey(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)

	withdrawalID, err := requests.RouteUUID(ctx, "idempotencyKey")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "idempotency_key must be a UUID")
	}

	w, err := ctrl.withdrawals.FindByIDAndWallet(ctx.Context(), withdrawalID, wallet.ID)
	if err != nil || w == nil {
		return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "withdrawal not found")
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

	return ctx.Response().Success().Json(response)
}
