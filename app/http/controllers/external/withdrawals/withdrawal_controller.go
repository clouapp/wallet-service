package withdrawals

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	withdrawalsrequests "github.com/macrowallets/waas/app/http/requests/withdrawals"
	resources "github.com/macrowallets/waas/app/http/resources/withdrawals"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/features"
	"github.com/macrowallets/waas/app/services/withdraw"
	"github.com/macrowallets/waas/app/services/withdrawalrecords"
)

// WithdrawalController serves the external withdrawal routes.
type WithdrawalController struct {
	records *withdrawalrecords.Records
	service *withdraw.Service
	flags   *features.Service
}

// NewWithdrawalController wires the external withdrawal handlers.
func NewWithdrawalController(records *withdrawalrecords.Records, service *withdraw.Service, flags *features.Service) *WithdrawalController {
	if records == nil {
		panic("external withdrawals controller: withdrawals service is required")
	}
	if service == nil {
		panic("external withdrawals controller: withdrawal service is required")
	}
	if flags == nil {
		panic("external withdrawals controller: feature flags are required")
	}
	return &WithdrawalController{records: records, service: service, flags: flags}
}

// Store godoc
//
//	@Summary		Create a withdrawal for a wallet
//	@Description	Initiates a new withdrawal. The withdrawal is created in 'pending' status and queued for approval/processing.
//	@Description	The access token itself is the authentication factor (reinforced by HMAC signing when the token has require_signature=true), so no TOTP is required. The withdrawal is attributed to the token's account. The wallet passphrase is verified before the withdrawal row is persisted.
//	@Tags			Wallet Withdrawals
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			walletId	path		string								true	"Wallet UUID"
//	@Param			request		body		withdrawalsrequests.StoreRequest	true	"Withdrawal payload"
//	@Success		201			{object}	resources.Withdrawal
//	@Failure		400			{object}	responses.ErrorBody
//	@Failure		403			{object}	responses.ErrorBody
//	@Failure		429			{object}	responses.ErrorBody	"Rate limit exceeded (too_many_requests, Retry-After header)"
//	@Router			/wallets/{walletId}/withdrawals [post]
func (c *WithdrawalController) Store(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)
	if response := controllers.BlockFlag(ctx, c.flags, controllers.AccountIDForWallet(ctx, wallet), features.FlagWithdrawalsEnabled, features.CodeWithdrawalsPaused, "create_wallet_withdrawal"); response != nil {
		return response
	}

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
		return mapError(ctx, err, "create_wallet_withdrawal")
	}

	if result.Replayed {
		return ctx.Response().Success().Json(resources.WithdrawalPtr(result.Withdrawal))
	}
	return ctx.Response().Status(http.StatusCreated).Json(resources.WithdrawalPtr(result.Withdrawal))
}

// ShowByKey godoc
//
//	@Summary		Look up a withdrawal by idempotency key
//	@Description	Returns the outcome of a withdrawal created with the given idempotency_key (the key is also the withdrawal id). Lets a client that lost the create response learn whether the withdrawal was broadcast or failed. Scoped to the token's account.
//	@Tags			Wallet Withdrawals
//	@Security		BearerAuth
//	@Produce		json
//	@Param			walletId		path		string	true	"Wallet UUID"
//	@Param			idempotencyKey	path		string	true	"Idempotency key (UUID) sent when the withdrawal was created"
//	@Success		200				{object}	resources.Lookup
//	@Failure		400				{object}	responses.ErrorBody	"idempotency_key must be a UUID"
//	@Failure		401				{object}	responses.ErrorBody
//	@Failure		404				{object}	responses.ErrorBody	"wallet not found / withdrawal not found"
//	@Failure		429				{object}	responses.ErrorBody	"Rate limit exceeded (too_many_requests, Retry-After header)"
//	@Router			/api/v1/wallets/{walletId}/withdrawals/{idempotencyKey} [get]
func (c *WithdrawalController) ShowByKey(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)

	withdrawalID, err := requests.RouteUUID(ctx, "idempotencyKey")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "idempotency_key must be a UUID")
	}

	outcome, err := c.records.LookupInWallet(ctx.Context(), wallet.ID, withdrawalID)
	if err != nil {
		return mapError(ctx, err, "lookup_withdrawal_transaction")
	}

	return ctx.Response().Success().Json(resources.NewLookup(outcome.Withdrawal, outcome.Transaction))
}
