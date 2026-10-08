package accounts

import (
	"errors"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	accountsvc "github.com/macrowallets/waas/app/services/account"
)

// LifecycleController is POST /v1/platform/accounts/{accountId}/freeze,
// /unfreeze, and /archive. S3.4.1 names accounts.lifecycle. A platform_admins
// row is the gate. The routes are not behind AccountContext, so a frozen or
// archived account can still be changed.
type LifecycleController struct {
	accounts *accountsvc.Service
}

// NewLifecycleController wires the platform account lifecycle handlers.
func NewLifecycleController(accounts *accountsvc.Service) *LifecycleController {
	if accounts == nil {
		panic("platform account lifecycle: account service is required")
	}
	return &LifecycleController{accounts: accounts}
}

type lifecycleBody struct {
	ID     uuid.UUID `json:"id"`
	Status string    `json:"status"`
}

// Freeze godoc
// @Summary      Freeze an account
// @Description  Sets accounts.status to frozen. Only a platform admin may call it. An unknown account is 404 before that check. The same status again does not write. The route is not behind AccountContext.
// @Tags         Platform Accounts
// @Security     BearerAuth
// @Produce      json
// @Param        accountId  path  string  true  "Account UUID"
// @Success      200  {object}  lifecycleBody
// @Failure      400  {object}  responses.ErrorBody
// @Failure      401  {object}  responses.ErrorBody
// @Failure      403  {object}  responses.ErrorBody
// @Failure      404  {object}  responses.ErrorBody
// @Router       /platform/accounts/{accountId}/freeze [post]
func (ctrl *LifecycleController) Freeze(ctx http.Context) http.Response {
	return ctrl.set(ctx, models.AccountStatusFrozen)
}

// Unfreeze godoc
// @Summary      Unfreeze an account
// @Description  Sets accounts.status to active. Only a platform admin may call it. An unknown account is 404 before that check. The same status again does not write. The route is not behind AccountContext, so a frozen account can be unfrozen.
// @Tags         Platform Accounts
// @Security     BearerAuth
// @Produce      json
// @Param        accountId  path  string  true  "Account UUID"
// @Success      200  {object}  lifecycleBody
// @Failure      400  {object}  responses.ErrorBody
// @Failure      401  {object}  responses.ErrorBody
// @Failure      403  {object}  responses.ErrorBody
// @Failure      404  {object}  responses.ErrorBody
// @Router       /platform/accounts/{accountId}/unfreeze [post]
func (ctrl *LifecycleController) Unfreeze(ctx http.Context) http.Response {
	return ctrl.set(ctx, models.StatusActive)
}

// Archive godoc
// @Summary      Archive an account
// @Description  Sets accounts.status to archived. Only a platform admin may call it. An unknown account is 404 before that check. The same status again does not write. The route is not behind AccountContext.
// @Tags         Platform Accounts
// @Security     BearerAuth
// @Produce      json
// @Param        accountId  path  string  true  "Account UUID"
// @Success      200  {object}  lifecycleBody
// @Failure      400  {object}  responses.ErrorBody
// @Failure      401  {object}  responses.ErrorBody
// @Failure      403  {object}  responses.ErrorBody
// @Failure      404  {object}  responses.ErrorBody
// @Router       /platform/accounts/{accountId}/archive [post]
func (ctrl *LifecycleController) Archive(ctx http.Context) http.Response {
	return ctrl.set(ctx, models.AccountStatusArchived)
}

func (ctrl *LifecycleController) set(ctx http.Context, status string) http.Response {
	actorID := middleware.SessionUserID(ctx)
	if actorID == uuid.Nil {
		return responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "unauthorized")
	}
	accountID, err := requests.RouteUUID(ctx, "accountId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid account id")
	}
	account, err := ctrl.accounts.SetPlatformLifecycle(ctx.Context(), actorID, accountID, status)
	if err != nil {
		return mapLifecycleError(ctx, err)
	}
	return responses.Send(ctx, http.StatusOK, lifecycleBody{ID: account.ID, Status: account.Status})
}

func mapLifecycleError(ctx http.Context, err error) http.Response {
	switch {
	case errors.Is(err, accountsvc.ErrPlatformLifecycleForbidden):
		return responses.Error(ctx, http.StatusForbidden, responses.CodeForbidden, accountsvc.ErrPlatformLifecycleForbidden.Error())
	case errors.Is(err, accountsvc.ErrAccountNotFound):
		return responses.Error(ctx, http.StatusNotFound, responses.CodeNotFound, accountsvc.ErrAccountNotFound.Error())
	default:
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternalError, "internal_error")
	}
}
