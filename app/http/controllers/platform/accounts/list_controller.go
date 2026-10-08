package accounts

import (
	"errors"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware"
	"github.com/macrowallets/waas/app/http/pagination"
	platformaccounts "github.com/macrowallets/waas/app/http/resources/platform/accounts"
	"github.com/macrowallets/waas/app/http/responses"
	accountsvc "github.com/macrowallets/waas/app/services/account"
)

// platformAccountsDefaultLimit matches GET /v1/platform/users: an omitted
// limit is 20 and an omitted offset is 0.
const platformAccountsDefaultLimit = 20

// ListController is GET /v1/platform/accounts. S3.4.1 names accounts.view.
// A platform_admins row is the gate.
type ListController struct {
	accounts *accountsvc.Service
}

// NewListController wires the platform account list.
func NewListController(accounts *accountsvc.Service) *ListController {
	if accounts == nil {
		panic("platform account list: account service is required")
	}
	return &ListController{accounts: accounts}
}

// Index godoc
// @Summary      List platform accounts
// @Description  Newest created_at first. Permission accounts.view; a platform admin may call it. The row is id, name, and status.
// @Tags         Platform Accounts
// @Security     BearerAuth
// @Produce      json
// @Param        limit   query  int  false  "Page size"
// @Param        offset  query  int  false  "Rows to skip"
// @Success      200  {object}  map[string]any
// @Failure      401  {object}  responses.ErrorBody
// @Failure      403  {object}  responses.ErrorBody
// @Router       /platform/accounts [get]
func (ctrl *ListController) Index(ctx http.Context) http.Response {
	actorID := middleware.SessionUserID(ctx)
	if actorID == uuid.Nil {
		return responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "unauthorized")
	}
	limit, offset := pagination.ParseParams(ctx, platformAccountsDefaultLimit)
	rows, total, err := ctrl.accounts.ListForPlatform(ctx.Context(), actorID, limit, offset)
	if errResp := mapListError(ctx, err); errResp != nil {
		return errResp
	}
	return responses.Send(ctx, http.StatusOK, pagination.Response(platformaccounts.AccountsFrom(rows), total, limit, offset))
}

func mapListError(ctx http.Context, err error) http.Response {
	if err == nil {
		return nil
	}
	if errors.Is(err, accountsvc.ErrPlatformViewForbidden) {
		return responses.Error(ctx, http.StatusForbidden, responses.CodeForbidden, accountsvc.ErrPlatformViewForbidden.Error())
	}
	return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternalError, "internal_error")
}
