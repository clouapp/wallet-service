package controllers

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/models"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	mpcpkg "github.com/macrowallets/waas/app/services/mpc"
	"github.com/macrowallets/waas/app/services/withdraw"
	"github.com/macrowallets/waas/app/services/withdrawalevents"
	"github.com/macrowallets/waas/pkg/types"
)

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
func ListWalletWithdrawals(ctx http.Context) http.Response {
	wallet := ctx.Value("wallet").(*models.Wallet)

	limit, offset := pagination.ParseParams(ctx, 50)
	status := ctx.Request().Query("status", "")
	withdrawals, total, err := container.Get().WithdrawalRepo.FindByWallet(wallet.ID, status, limit, offset)
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to fetch withdrawals"})
	}
	return ctx.Response().Json(http.StatusOK, pagination.Response(withdrawals, total, limit, offset))
}

func EstimateWithdrawalFee(ctx http.Context) http.Response {
	wallet := ctx.Value("wallet").(*models.Wallet)

	var req requests.EstimateWithdrawalRequest
	if resp := validateRequest(ctx, &req); resp != nil {
		return resp
	}

	adapter, err := container.Get().Registry.ChainForWallet(wallet)
	if err != nil {
		return ctx.Response().Json(http.StatusUnprocessableEntity, http.Json{
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
		return ctx.Response().Json(http.StatusUnprocessableEntity, http.Json{
			"error": "fee estimation unavailable",
			"code":  "FEE_ESTIMATE_FAILED",
		})
	}

	return ctx.Response().Json(http.StatusOK, estimate)
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
func CreateWalletWithdrawal(ctx http.Context) http.Response {
	wallet := ctx.Value("wallet").(*models.Wallet)

	var req requests.CreateWalletWithdrawalRequest
	if resp := validateRequest(ctx, &req); resp != nil {
		return resp
	}

	callerUserID, hasUser := ctx.Value("user_id").(uuid.UUID)
	isDashboardCaller := hasUser && callerUserID != uuid.Nil

	if isDashboardCaller {
		user, err := container.Get().UserRepo.FindByID(callerUserID)
		if err != nil || user == nil {
			return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "user not found"})
		}

		if !user.TotpEnabled {
			return ctx.Response().Json(http.StatusForbidden, http.Json{"error": "2FA must be enabled before withdrawing"})
		}

		decryptedSecret, err := facades.Crypt().DecryptString(user.TotpSecret)
		if err != nil {
			return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "internal error"})
		}
		authService := authsvc.NewService()
		if !authService.VerifyTOTP(decryptedSecret, req.TotpCode) {
			return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "invalid 2FA code"})
		}
	} else {
		accountID, hasAccount := ctx.Value("account_id").(uuid.UUID)
		if !hasAccount || accountID == uuid.Nil {
			return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "unauthenticated"})
		}
	}

	if errResp := verifyWalletPassphrase(ctx, wallet, req.Passphrase); errResp != nil {
		return errResp
	}

	adapter, err := container.Get().Registry.ChainForWallet(wallet)
	if err != nil {
		return ctx.Response().Json(http.StatusUnprocessableEntity, http.Json{"error": err.Error()})
	}

	chainEntity, chainErr := container.Get().ChainRepo.FindByID(wallet.Chain)
	if chainErr != nil || chainEntity == nil {
		return ctx.Response().Json(http.StatusUnprocessableEntity, http.Json{"error": "chain not found"})
	}
	resolved, resolveErr := withdraw.ResolveWithdrawalAmount(
		wallet.Chain,
		adapter.NativeAsset(),
		chainEntity.NativeDecimals,
		req.Asset,
		req.Amount,
		container.Get().Registry.TokensForChain(wallet.Chain),
	)
	if resolveErr != nil {
		return ctx.Response().Json(http.StatusUnprocessableEntity, http.Json{"error": resolveErr.Error()})
	}

	callerAccountID, _ := ctx.Value("account_id").(uuid.UUID)
	if callerAccountID == uuid.Nil && wallet.AccountID != nil {
		callerAccountID = *wallet.AccountID
	}

	withdrawalID, err := withdrawalIDFromIdempotencyKey(req.IdempotencyKey)
	if err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": err.Error()})
	}
	idempotencyKey := req.IdempotencyKey
	if idempotencyKey == "" {
		idempotencyKey = withdrawalID.String()
	}
	if wallet.DepositAddress == nil {
		return ctx.Response().Json(http.StatusUnprocessableEntity, http.Json{
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

	existing, findErr := container.Get().WithdrawalRepo.FindByIDAndWallet(withdrawalID, wallet.ID)
	if findErr != nil {
		return MapInternalError(ctx, findErr, "find_idempotent_withdrawal")
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
		if createErr := container.Get().WithdrawalRepo.Create(w); createErr != nil {
			return MapInternalError(ctx, createErr, "create_broadcasting_withdrawal")
		}
	} else {
		if updateErr := container.Get().WithdrawalRepo.UpdateFields(w.ID, map[string]any{
			"status":              "broadcasting",
			"failure_reason":      nil,
			"amount":              req.Amount,
			"destination_address": req.DestinationAddress,
			"fee_estimate":        feeEstimate,
			"note":                req.Note,
		}); updateErr != nil {
			return MapInternalError(ctx, updateErr, "retry_broadcasting_withdrawal")
		}
		w.Status = "broadcasting"
	}

	tx, _, err := container.Get().WithdrawalService.Request(ctx.Context(), withdraw.WithdrawRequest{
		WalletID:        wallet.ID,
		ToAddress:       req.DestinationAddress,
		Amount:          resolved.BaseUnits.String(),
		Asset:           resolved.WalletAsset,
		Passphrase:      req.Passphrase,
		IdempotencyKey:  idempotencyKey,
		CallerAccountID: callerAccountID,
	})
	if err != nil {
		failureCode := withdrawalFailureCode(err)
		if updateErr := container.Get().WithdrawalRepo.UpdateFields(w.ID, map[string]any{
			"status":         models.WithdrawalStatusFailed,
			"failure_reason": failureCode,
		}); updateErr != nil {
			return MapInternalError(
				ctx,
				fmt.Errorf("execute withdrawal: %v; mark failed: %w", err, updateErr),
				"mark_withdrawal_failed",
			)
		}
		w.Status = models.WithdrawalStatusFailed
		publishWithdrawalFailed(ctx, w, failureCode, withdrawalevents.FailedAttempt{
			Chain:     wallet.Chain,
			Asset:     resolved.WalletAsset,
			BaseUnits: resolved.BaseUnits,
		})
		if resp := MapSweepError(ctx, err); resp != nil {
			return resp
		}
		switch {
		case errors.Is(err, withdraw.ErrInvalidPassphrase):
			return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": err.Error()})
		case errors.Is(err, withdraw.ErrPassphraseTooShort):
			return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": err.Error()})
		case errors.Is(err, withdraw.ErrInsufficientFunds):
			return ctx.Response().Json(http.StatusUnprocessableEntity, http.Json{"error": err.Error()})
		case errors.Is(err, withdraw.ErrConcurrentWithdraw):
			return ctx.Response().Json(http.StatusConflict, http.Json{"error": err.Error()})
		case errors.Is(err, withdraw.ErrTooManyAttempts):
			return ctx.Response().Json(http.StatusTooManyRequests, http.Json{"error": err.Error()})
		default:
			return MapInternalError(ctx, err, "create_wallet_withdrawal")
		}
	}

	w.Status = "broadcast"
	if tx != nil {
		w.TransactionID = &tx.ID
		w.TxHash = tx.TxHash
	}
	if updateErr := container.Get().WithdrawalRepo.UpdateFields(w.ID, map[string]any{
		"status":         w.Status,
		"transaction_id": w.TransactionID,
	}); updateErr != nil {
		return MapInternalError(ctx, updateErr, "persist_broadcast_withdrawal")
	}
	publishWithdrawalBroadcast(ctx, w, tx)
	return ctx.Response().Json(http.StatusCreated, w)
}

// Webhook publishing never changes the HTTP outcome: the withdrawal is already
// persisted, and the confirmation tracker backfill repairs a lost confirmation.
func publishWithdrawalBroadcast(ctx http.Context, w *models.Withdrawal, tx *models.Transaction) {
	publisher := container.Get().WithdrawalEvents
	if publisher == nil || tx == nil {
		return
	}
	if err := publisher.PublishBroadcast(ctx.Context(), w, tx); err != nil {
		slog.Error("publish withdrawal.broadcast", "withdrawal_id", w.ID, "error", err)
	}
}

func publishWithdrawalFailed(ctx http.Context, w *models.Withdrawal, failureCode string, attempt withdrawalevents.FailedAttempt) {
	publisher := container.Get().WithdrawalEvents
	if publisher == nil {
		return
	}
	if err := publisher.PublishFailed(ctx.Context(), w, failureCode, attempt); err != nil {
		slog.Error("publish withdrawal.failed", "withdrawal_id", w.ID, "error", err)
	}
}

func withdrawalIDFromIdempotencyKey(idempotencyKey string) (uuid.UUID, error) {
	if strings.TrimSpace(idempotencyKey) == "" {
		return uuid.New(), nil
	}
	id, err := uuid.Parse(idempotencyKey)
	if err != nil {
		return uuid.Nil, fmt.Errorf("idempotency_key must be a UUID")
	}
	return id, nil
}

func verifyWalletPassphrase(ctx http.Context, wallet *models.Wallet, passphrase string) http.Response {
	rdb := container.Get().Redis
	key := fmt.Sprintf("vault:ratelimit:passphrase:%s", wallet.ID)

	if rdb != nil {
		count, err := rdb.Get(ctx.Context(), key).Int()
		if err == nil && count >= 5 {
			return ctx.Response().Json(http.StatusTooManyRequests, http.Json{"error": "too many failed attempts, try again later"})
		}
	}

	shareA, decErr := wallet.DecryptShareA(passphrase)
	if decErr != nil {
		if errors.Is(decErr, mpcpkg.ErrInvalidPassphrase) {
			if rdb != nil {
				pipe := rdb.Pipeline()
				pipe.Incr(ctx.Context(), key)
				pipe.Expire(ctx.Context(), key, 60*time.Second)
				_, _ = pipe.Exec(ctx.Context())
			}
			return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "invalid passphrase"})
		}
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "internal error"})
	}
	for i := range shareA {
		shareA[i] = 0
	}

	return nil
}

// GetWalletWithdrawal godoc
// @Summary      Get a single wallet withdrawal
// @Description  Returns a specific withdrawal by ID scoped to the wallet
// @Tags         Wallet Withdrawals
// @Security     BearerAuth
// @Produce      json
// @Param        walletId      path  string  true  "Wallet UUID"
// @Param        withdrawalId  path  string  true  "Withdrawal UUID"
// @Success      200  {object}  models.Withdrawal
// @Failure      403  {object}  ErrorResponse
// @Failure      404  {object}  ErrorResponse
// @Router       /wallets/{walletId}/withdrawals/{withdrawalId} [get]
func GetWalletWithdrawal(ctx http.Context) http.Response {
	wallet := ctx.Value("wallet").(*models.Wallet)

	withdrawalIDStr := ctx.Request().Route("withdrawalId")
	withdrawalID, err := uuid.Parse(withdrawalIDStr)
	if err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid withdrawal id"})
	}

	w, err := container.Get().WithdrawalRepo.FindByIDAndWallet(withdrawalID, wallet.ID)
	if err != nil || w == nil {
		return ctx.Response().Json(http.StatusNotFound, http.Json{"error": "withdrawal not found"})
	}
	return ctx.Response().Json(http.StatusOK, w)
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
func GetWalletWithdrawalByIdempotencyKey(ctx http.Context) http.Response {
	wallet := ctx.Value("wallet").(*models.Wallet)

	withdrawalID, err := uuid.Parse(strings.TrimSpace(ctx.Request().Route("idempotencyKey")))
	if err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "idempotency_key must be a UUID"})
	}

	w, err := container.Get().WithdrawalRepo.FindByIDAndWallet(withdrawalID, wallet.ID)
	if err != nil || w == nil {
		return ctx.Response().Json(http.StatusNotFound, http.Json{"error": "withdrawal not found"})
	}

	response := WithdrawalLookupResponse{
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
		tx, txErr := container.Get().TransactionRepo.FindByID(*w.TransactionID)
		if txErr != nil {
			return MapInternalError(ctx, txErr, "lookup_withdrawal_transaction")
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

// CancelWalletWithdrawal godoc
// @Summary      Cancel a pending withdrawal
// @Description  Cancels a withdrawal that is still in 'pending' status. Requires the creator or an owner/admin.
// @Tags         Wallet Withdrawals
// @Security     BearerAuth
// @Produce      json
// @Param        walletId      path  string  true  "Wallet UUID"
// @Param        withdrawalId  path  string  true  "Withdrawal UUID"
// @Success      200  {object}  models.Withdrawal
// @Failure      403  {object}  ErrorResponse
// @Failure      404  {object}  ErrorResponse
// @Failure      422  {object}  ErrorResponse  "Withdrawal cannot be cancelled in current state"
// @Router       /wallets/{walletId}/withdrawals/{withdrawalId}/cancel [post]
func CancelWalletWithdrawal(ctx http.Context) http.Response {
	wallet := ctx.Value("wallet").(*models.Wallet)

	withdrawalIDStr := ctx.Request().Route("withdrawalId")
	withdrawalID, err := uuid.Parse(withdrawalIDStr)
	if err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid withdrawal id"})
	}

	w, err := container.Get().WithdrawalRepo.FindByIDAndWallet(withdrawalID, wallet.ID)
	if err != nil || w == nil {
		return ctx.Response().Json(http.StatusNotFound, http.Json{"error": "withdrawal not found"})
	}

	if w.Status != "pending" {
		return ctx.Response().Json(http.StatusUnprocessableEntity, http.Json{
			"error": "only pending withdrawals can be cancelled",
		})
	}

	creatorID := uuid.Nil
	if w.CreatedBy != nil {
		creatorID = *w.CreatedBy
	}
	if resp := authorize(ctx, "wallet.cancel-withdrawal", map[string]any{"wallet_id": wallet.ID, "creator_id": creatorID}); resp != nil {
		return resp
	}

	if err := container.Get().WithdrawalRepo.UpdateStatus(w.ID, "cancelled"); err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to cancel withdrawal"})
	}
	w.Status = "cancelled"
	return ctx.Response().Json(http.StatusOK, w)
}

// ---- Request/Response types ----

type CreateWalletWithdrawalSwagger struct {
	Amount             string `json:"amount" example:"0.001"`
	DestinationAddress string `json:"destination_address" example:"bc1q..."`
	Note               string `json:"note,omitempty" example:"Monthly payment"`
	Passphrase         string `json:"passphrase" example:"my-secure-wallet-passphrase"`
	TotpCode           string `json:"totp_code" example:"123456"`
	Asset              string `json:"asset,omitempty" example:"USDT"`
}

type EstimateWithdrawalSwagger struct {
	Amount             string `json:"amount" example:"0.001"`
	DestinationAddress string `json:"destination_address" example:"tb1q..."`
}

type WithdrawalListResponse struct {
	Data []models.Withdrawal `json:"data"`
}
