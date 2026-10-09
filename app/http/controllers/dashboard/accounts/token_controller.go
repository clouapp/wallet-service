package accounts

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/requests"
	accountsrequests "github.com/macrowallets/waas/app/http/requests/dashboard/accounts"
	resources "github.com/macrowallets/waas/app/http/resources/dashboard/accounts"
	"github.com/macrowallets/waas/app/http/responses"
	accountsvc "github.com/macrowallets/waas/app/services/account"
	"github.com/macrowallets/waas/app/services/apitoken"
)

// TokenController lists, mints and revokes the API tokens of the account in
// scope. tokens.read and tokens.write are route middleware.
type TokenController struct {
	accounts *accountsvc.Service
	tokens   *apitoken.Service
}

// NewTokenController wires the token handlers: the account service lists and
// revokes, the token service mints.
func NewTokenController(accounts *accountsvc.Service, tokens *apitoken.Service) *TokenController {
	if accounts == nil {
		panic("dashboard token controller: account service is required")
	}
	if tokens == nil {
		panic("dashboard token controller: token service is required")
	}
	return &TokenController{accounts: accounts, tokens: tokens}
}

// Index godoc
//
//	@Summary		List API access tokens for an account
//	@Description	Returns the account's API tokens. Requires tokens.read.
//	@Tags			Accounts
//	@Security		BearerAuth
//	@Produce		json
//	@Param			accountId	path		string	true	"Account UUID"
//	@Success		200			{object}	AccessTokenListResponse
//	@Failure		403			{object}	responses.ErrorBody
//	@Router			/accounts/{accountId}/tokens [get]
func (c *TokenController) Index(ctx http.Context) http.Response {
	account := requestctx.MustAccount(ctx)

	limit, offset := pagination.ParseParams(ctx, 20)

	tokens, total, err := c.accounts.ListAccessTokens(ctx.Context(), account.ID, limit, offset)
	if err != nil {
		return mapError(ctx, err, "failed to fetch tokens")
	}

	return ctx.Response().Success().Json(pagination.Response(resources.AccessTokensFrom(tokens), total, limit, offset))
}

// Store godoc
//
//	@Summary		Create an API access token for an account
//	@Description	Creates a named access token. The raw token is returned once — store it safely. Requires tokens.write.
//	@Tags			Accounts
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			accountId	path		string						true	"Account UUID"
//	@Param			request		body		CreateAccountTokenSwagger	true	"Token payload"
//	@Success		201			{object}	CreateAccountTokenResponse
//	@Failure		400			{object}	responses.ErrorBody
//	@Failure		403			{object}	responses.ErrorBody
//	@Router			/accounts/{accountId}/tokens [post]
func (c *TokenController) Store(ctx http.Context) http.Response {
	account := requestctx.MustAccount(ctx)
	creatorID := requestctx.MustUserID(ctx)

	var req accountsrequests.CreateAccountTokenRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}

	minted, err := c.tokens.Mint(ctx.Context(), apitoken.MintInput{
		AccountID:        account.ID,
		CreatedBy:        creatorID,
		Name:             req.Name,
		ValidUntil:       req.ValidUntil,
		RequireSignature: req.RequireSignature,
		Permissions:      req.Permissions,
		IPCIDR:           req.IpCidr,
		SpendingLimit:    req.SpendingLimit,
	})
	if err != nil {
		return mapError(ctx, err, "failed to create token")
	}

	return ctx.Response().Status(http.StatusCreated).Json(resources.NewMintedToken(minted))
}

// Destroy godoc
//
//	@Summary		Revoke an API access token
//	@Description	Soft-revokes an access token by ID. The row stays for audit. Requires tokens.write.
//	@Tags			Accounts
//	@Security		BearerAuth
//	@Produce		json
//	@Param			accountId	path	string	true	"Account UUID"
//	@Param			tokenId		path	string	true	"Token UUID"
//	@Success		204			"No content"
//	@Failure		403			{object}	responses.ErrorBody
//	@Failure		404			{object}	responses.ErrorBody
//	@Router			/accounts/{accountId}/tokens/{tokenId} [delete]
func (c *TokenController) Destroy(ctx http.Context) http.Response {
	account := requestctx.MustAccount(ctx)
	callerID := requestctx.MustUserID(ctx)

	tokenID, err := requests.RouteUUID(ctx, "tokenId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid token id")
	}

	if err := c.accounts.RevokeAccessToken(ctx.Context(), account.ID, callerID, tokenID); err != nil {
		return mapError(ctx, err, "failed to revoke token")
	}

	return ctx.Response().NoContent()
}
