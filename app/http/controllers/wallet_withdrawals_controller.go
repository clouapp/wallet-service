package controllers

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/models"
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
	wallet, _, _, errResp := walletFromParam(ctx)
	if errResp != nil {
		return errResp
	}

	limit, offset := pagination.ParseParams(ctx, 50)
	status := ctx.Request().Query("status", "")
	withdrawals, total, err := container.Get().WithdrawalRepo.FindByWallet(wallet.ID, status, limit, offset)
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to fetch withdrawals"})
	}
	return ctx.Response().Json(http.StatusOK, pagination.Response(withdrawals, total, limit, offset))
}

// CreateWalletWithdrawal godoc
// @Summary      Create a withdrawal for a wallet
// @Description  Initiates a new withdrawal. The withdrawal is created in 'pending' status and queued for approval/processing.
// @Tags         Wallet Withdrawals
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        walletId  path      string                    true  "Wallet UUID"
// @Param        request   body      CreateWalletWithdrawalRequest  true  "Withdrawal payload"
// @Success      201  {object}  models.Withdrawal
// @Failure      400  {object}  ErrorResponse
// @Failure      403  {object}  ErrorResponse
// @Router       /wallets/{walletId}/withdrawals [post]
func CreateWalletWithdrawal(ctx http.Context) http.Response {
	wallet, _, _, errResp := walletFromParam(ctx)
	if errResp != nil {
		return errResp
	}

	// Any member may initiate; approval is handled separately
	callerID, _ := ctx.Value("user_id").(uuid.UUID)

	var req CreateWalletWithdrawalRequest
	if err := ctx.Request().Bind(&req); err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid request body"})
	}
	if req.Amount == "" || req.DestinationAddress == "" {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "amount and destination_address are required"})
	}

	w := &models.Withdrawal{
		ID:                 uuid.New(),
		WalletID:           wallet.ID,
		Status:             "pending",
		Amount:             req.Amount,
		DestinationAddress: req.DestinationAddress,
		Note:               req.Note,
		CreatedBy:          &callerID,
	}
	if wallet.AccountID != nil {
		w.AccountID = wallet.AccountID
	}

	if err := container.Get().WithdrawalRepo.Create(w); err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to create withdrawal"})
	}
	return ctx.Response().Json(http.StatusCreated, w)
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
	wallet, _, _, errResp := walletFromParam(ctx)
	if errResp != nil {
		return errResp
	}

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
	wallet, accRole, walletRole, errResp := walletFromParam(ctx)
	if errResp != nil {
		return errResp
	}

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

	callerID, _ := ctx.Value("user_id").(uuid.UUID)
	isOwner := isWalletAdmin(accRole, walletRole)
	isCreator := w.CreatedBy != nil && *w.CreatedBy == callerID
	if !isOwner && !isCreator {
		return ctx.Response().Json(http.StatusForbidden, http.Json{"error": "only the creator or an owner/admin may cancel this withdrawal"})
	}

	if err := container.Get().WithdrawalRepo.UpdateStatus(w.ID, "cancelled"); err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to cancel withdrawal"})
	}
	w.Status = "cancelled"
	return ctx.Response().Json(http.StatusOK, w)
}

// ---- Request/Response types ----

type CreateWalletWithdrawalRequest struct {
	Amount             string `json:"amount" example:"0.001"`
	DestinationAddress string `json:"destination_address" example:"bc1q..."`
	Note               string `json:"note,omitempty" example:"Monthly payment"`
}

type WithdrawalListResponse struct {
	Data []models.Withdrawal `json:"data"`
}
