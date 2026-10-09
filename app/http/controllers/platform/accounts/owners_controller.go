package accounts

import (
	"errors"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware"
	"github.com/macrowallets/waas/app/http/requests"
	platformaccounts "github.com/macrowallets/waas/app/http/resources/platform/accounts"
	"github.com/macrowallets/waas/app/http/responses"
	accountsvc "github.com/macrowallets/waas/app/services/account"
)

// OwnersController is POST /v1/platform/accounts/{accountId}/owners.
// S3.4.1 names attach owner — recovery and accounts.owners. A platform_admins
// row is the gate. The route is not behind AccountContext.
type OwnersController struct {
	accounts *accountsvc.Service
}

// NewOwnersController wires the platform attach-owner handler.
func NewOwnersController(accounts *accountsvc.Service) *OwnersController {
	if accounts == nil {
		panic("platform account owners: account service is required")
	}
	return &OwnersController{accounts: accounts}
}

// Attach godoc
// @Summary      Attach an account owner
// @Description  Links an existing user as an active owner. The body is email, matching account member add. Only a platform admin may call it. An unknown account is 404 for a platform admin. An unknown user is 404 for a platform admin. The same active owner again does not write and answers 204. A new or restored membership answers 201 with the membership row. The route is not behind AccountContext, so a frozen account can still be recovered. Password hashes, TOTP secrets, recovery codes, and token material are omitted.
// @Tags         Platform Accounts
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        accountId  path  string  true  "Account UUID"
// @Param        request    body  requests.AttachPlatformOwnerRequest  true  "Existing user email"
// @Success      201  {object}  platformaccounts.AccountUser
// @Success      204  "Already the active owner"
// @Failure      400  {object}  responses.ErrorBody
// @Failure      401  {object}  responses.ErrorBody
// @Failure      403  {object}  responses.ErrorBody
// @Failure      404  {object}  responses.ErrorBody
// @Failure      422  {object}  responses.ErrorBody
// @Router       /platform/accounts/{accountId}/owners [post]
func (ctrl *OwnersController) Attach(ctx http.Context) http.Response {
	actorID := middleware.SessionUserID(ctx)
	if actorID == uuid.Nil {
		return responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "unauthorized")
	}
	accountID, err := requests.RouteUUID(ctx, "accountId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid account id")
	}
	var req requests.AttachPlatformOwnerRequest
	if errResp := requests.Validate(ctx, &req); errResp != nil {
		return errResp
	}
	member, changed, err := ctrl.accounts.AttachOwnerForPlatform(ctx.Context(), actorID, accountID, req.Email)
	if errResp := mapAttachOwnerError(ctx, err); errResp != nil {
		return errResp
	}
	if member == nil {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternalError, "internal_error")
	}
	if !changed {
		return ctx.Response().NoContent()
	}
	return ctx.Response().Status(http.StatusCreated).Json(platformaccounts.AccountUserFrom(*member))
}

func mapAttachOwnerError(ctx http.Context, err error) http.Response {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, accountsvc.ErrPlatformOwnersForbidden):
		return responses.Error(ctx, http.StatusForbidden, responses.CodeForbidden, accountsvc.ErrPlatformOwnersForbidden.Error())
	case errors.Is(err, accountsvc.ErrAccountNotFound):
		return responses.Error(ctx, http.StatusNotFound, responses.CodeNotFound, accountsvc.ErrAccountNotFound.Error())
	case errors.Is(err, accountsvc.ErrPlatformOwnerUserNotFound):
		return responses.Error(ctx, http.StatusNotFound, responses.CodeNotFound, accountsvc.ErrPlatformOwnerUserNotFound.Error())
	default:
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternalError, "internal_error")
	}
}
