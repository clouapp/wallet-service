package withdrawals

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/middleware"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/requests"
	withdrawalsrequests "github.com/macrowallets/waas/app/http/requests/withdrawals"
	resources "github.com/macrowallets/waas/app/http/resources/withdrawals"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/withdraw"
	"github.com/macrowallets/waas/app/services/withdrawalrecords"
)

// WithdrawalController serves the dashboard withdrawal routes.
type WithdrawalController struct {
	records *withdrawalrecords.Records
	service *withdraw.Service
}

// NewWithdrawalController wires the dashboard withdrawal handlers.
func NewWithdrawalController(records *withdrawalrecords.Records, service *withdraw.Service) *WithdrawalController {
	if records == nil {
		panic("dashboard withdrawals controller: withdrawals service is required")
	}
	if service == nil {
		panic("dashboard withdrawals controller: withdrawal service is required")
	}
	return &WithdrawalController{records: records, service: service}
}

// Index godoc
//
//	@Summary		List withdrawals for a wallet
//	@Description	Returns a paginated list of withdrawals for a specific wallet
//	@Tags			Wallet Withdrawals
//	@Security		BearerAuth
//	@Produce		json
//	@Param			walletId	path		string	true	"Wallet UUID"
//	@Param			status		query		string	false	"Status filter"	Enums(pending,approved,rejected,broadcast,confirmed,failed)
//	@Param			limit		query		int		false	"Max results (default 50)"
//	@Param			offset		query		int		false	"Pagination offset"
//	@Success		200			{object}	resources.List
//	@Failure		403			{object}	responses.ErrorBody
//	@Failure		404			{object}	responses.ErrorBody
//	@Router			/wallets/{walletId}/withdrawals [get]
func (c *WithdrawalController) Index(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)

	limit, offset := pagination.ParseParams(ctx, 50)
	status := ctx.Request().Query("status")

	rows, total, err := c.records.FindByWallet(ctx.Context(), wallet.ID, status, limit, offset)
	if err != nil {
		return mapError(ctx, err, "fetch withdrawals")
	}

	return ctx.Response().Success().Json(pagination.Response(resources.WithdrawalsFrom(rows), total, limit, offset))
}

// Estimate prices a native-coin transfer to the destination address without
// signing or broadcasting. POST /wallets/{walletId}/withdrawals/estimate is not
// part of the documented API.
func (c *WithdrawalController) Estimate(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)

	var req withdrawalsrequests.EstimateRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}

	estimate, err := c.service.EstimateNativeFee(ctx.Context(), wallet.Chain, req.DestinationAddress)
	if err != nil {
		return mapError(ctx, err, "estimate withdrawal fee")
	}

	return ctx.Response().Success().Json(resources.NewFeeEstimate(estimate))
}

// Store godoc
//
//	@Summary		Create a withdrawal for a wallet
//	@Description	Initiates a new withdrawal. The withdrawal is created in 'pending' status and queued for approval/processing.
//	@Description	The caller is a human operator, so the TOTP code is required; the wallet passphrase is verified before the withdrawal row is persisted.
//	@Tags			Wallet Withdrawals
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			walletId	path		string								true	"Wallet UUID"
//	@Param			request		body		withdrawalsrequests.StoreRequest	true	"Withdrawal payload"
//	@Success		201			{object}	resources.Withdrawal
//	@Failure		400			{object}	responses.ErrorBody
//	@Failure		403			{object}	responses.ErrorBody
//	@Router			/wallets/{walletId}/withdrawals [post]
func (c *WithdrawalController) Store(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)

	var req withdrawalsrequests.StoreRequest
	defer controllers.DiscardPassphrase(&req.Passphrase)
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}
	userID, _ := requestctx.UserID(ctx)
	accountID, _ := requestctx.AccountID(ctx)
	token, _ := requestctx.APIToken(ctx)

	result, err := c.service.Submit(ctx.Context(), withdraw.SubmitInput{
		Wallet:             wallet,
		CallerUserID:       userID,
		CallerAccountID:    accountID,
		APIToken:           token,
		TotpCode:           req.TotpCode,
		Passphrase:         req.Passphrase,
		Asset:              req.Asset,
		Amount:             req.Amount,
		DestinationAddress: req.DestinationAddress,
		Note:               req.Note,
		IdempotencyKey:     req.IdempotencyKey,
	})
	if err != nil {
		return mapError(ctx, err, "create withdrawal")
	}

	if result.Replayed {
		return ctx.Response().Success().Json(resources.WithdrawalPtr(result.Withdrawal))
	}
	return ctx.Response().Status(http.StatusCreated).Json(resources.WithdrawalPtr(result.Withdrawal))
}

// Show godoc
//
//	@Summary		Get a single wallet withdrawal
//	@Description	Returns a specific withdrawal by ID scoped to the wallet
//	@Tags			Wallet Withdrawals
//	@Security		BearerAuth
//	@Produce		json
//	@Param			walletId		path		string	true	"Wallet UUID"
//	@Param			withdrawalId	path		string	true	"Withdrawal UUID"
//	@Success		200				{object}	resources.Withdrawal
//	@Failure		403				{object}	responses.ErrorBody
//	@Failure		404				{object}	responses.ErrorBody
//	@Router			/wallets/{walletId}/withdrawals/{withdrawalId} [get]
func (c *WithdrawalController) Show(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)

	withdrawalID, err := requests.RouteUUID(ctx, "withdrawalId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid withdrawal id")
	}

	withdrawal, err := c.records.FindInWallet(ctx.Context(), wallet.ID, withdrawalID)
	if err != nil {
		return mapError(ctx, err, "fetch withdrawal")
	}

	return ctx.Response().Success().Json(resources.WithdrawalPtr(withdrawal))
}

// ShowInAccount godoc
//
//	@Summary		Get a withdrawal by id
//	@Description	Returns one withdrawal for the account in X-Account-Id. A missing id, or a withdrawal outside that account, is not found.
//	@Tags			Wallet Withdrawals
//	@Security		BearerAuth
//	@Produce		json
//	@Param			withdrawalId	path		string	true	"Withdrawal UUID"
//	@Param			X-Account-Id	header		string	true	"Account UUID"
//	@Success		200				{object}	resources.Withdrawal
//	@Failure		401				{object}	responses.ErrorBody
//	@Failure		404				{object}	responses.ErrorBody
//	@Router			/withdrawals/{withdrawalId} [get]
func (c *WithdrawalController) ShowInAccount(ctx http.Context) http.Response {
	accountID, ok := requestctx.AccountID(ctx)
	if !ok || accountID == uuid.Nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "X-Account-Id header is required")
	}

	withdrawalID, err := requests.RouteUUID(ctx, "withdrawalId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid withdrawal id")
	}

	withdrawal, err := c.records.FindInAccount(ctx.Context(), accountID, withdrawalID)
	if err != nil {
		return mapError(ctx, err, "fetch withdrawal")
	}

	return ctx.Response().Success().Json(resources.WithdrawalPtr(withdrawal))
}

// Cancel godoc
//
//	@Summary		Cancel a pending withdrawal
//	@Description	Cancels a withdrawal that is still in 'pending' status. Requires the creator or an owner/admin.
//	@Tags			Wallet Withdrawals
//	@Security		BearerAuth
//	@Produce		json
//	@Param			walletId		path		string	true	"Wallet UUID"
//	@Param			withdrawalId	path		string	true	"Withdrawal UUID"
//	@Success		200				{object}	resources.Withdrawal
//	@Failure		403				{object}	responses.ErrorBody
//	@Failure		404				{object}	responses.ErrorBody
//	@Failure		422				{object}	responses.ErrorBody	"Withdrawal cannot be cancelled in current state"
//	@Router			/wallets/{walletId}/withdrawals/{withdrawalId}/cancel [post]
func (c *WithdrawalController) Cancel(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)
	actorID := middleware.SessionUserID(ctx)

	withdrawalID, err := requests.RouteUUID(ctx, "withdrawalId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid withdrawal id")
	}

	withdrawal, err := c.records.CancelPending(ctx.Context(), wallet, actorID, withdrawalID)
	if err != nil {
		return mapError(ctx, err, "cancel withdrawal")
	}

	return ctx.Response().Success().Json(resources.WithdrawalPtr(withdrawal))
}
