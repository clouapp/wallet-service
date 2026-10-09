package accounts

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	accountsrequests "github.com/macrowallets/waas/app/http/requests/platform/accounts"
	resources "github.com/macrowallets/waas/app/http/resources/platform/accounts"
	"github.com/macrowallets/waas/app/http/responses"
	accountsvc "github.com/macrowallets/waas/app/services/account"
)

// OwnerController is POST /v1/platform/accounts/{accountId}/owners.
// S3.4.1 names attach owner — recovery and accounts.owners. A platform_admins
// row is the gate. The route is not behind AccountContext.
type OwnerController struct {
	accounts *accountsvc.Service
}

// NewOwnerController wires the platform attach-owner handler.
func NewOwnerController(accounts *accountsvc.Service) *OwnerController {
	if accounts == nil {
		panic("platform account owners: account service is required")
	}
	return &OwnerController{accounts: accounts}
}

// Attach godoc
//
//	@Summary		Attach an account owner
//	@Description	Links an existing user as an active owner. The body is email, matching account member add. Only a platform admin may call it. An unknown account is 404 for a platform admin. An unknown user is 404 for a platform admin. The same active owner again does not write and answers 204. A new or restored membership answers 201 with the membership row. The route is not behind AccountContext, so a frozen account can still be recovered. Password hashes, TOTP secrets, recovery codes, and token material are omitted.
//	@Tags			Platform Accounts
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			accountId	path		string								true	"Account UUID"
//	@Param			request		body		accountsrequests.AttachOwnerRequest	true	"Existing user email"
//	@Success		201			{object}	resources.AccountUser
//	@Success		204			"Already the active owner"
//	@Failure		400			{object}	responses.ErrorBody
//	@Failure		401			{object}	responses.ErrorBody
//	@Failure		403			{object}	responses.ErrorBody
//	@Failure		404			{object}	responses.ErrorBody
//	@Failure		422			{object}	responses.ErrorBody
//	@Router			/platform/accounts/{accountId}/owners [post]
func (c *OwnerController) Attach(ctx http.Context) http.Response {
	actorID := requestctx.MustUserID(ctx)

	accountID, err := requests.RouteUUID(ctx, "accountId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid account id")
	}
	var req accountsrequests.AttachOwnerRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}

	member, changed, err := c.accounts.AttachOwnerForPlatform(ctx.Context(), actorID, accountID, req.Email)
	if err != nil {
		return mapError(ctx, err, "attach platform account owner")
	}

	if !changed {
		return ctx.Response().NoContent()
	}
	return ctx.Response().Status(http.StatusCreated).Json(resources.AccountUserFrom(*member))
}
