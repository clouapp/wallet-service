package activity

import (
	"errors"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	activitysvc "github.com/macrowallets/waas/app/services/activity"
)

const activityPageSize = 20

// ActivityController lists one account's activity log.
type ActivityController struct {
	activity *activitysvc.Service
}

// NewActivityController wires the dashboard activity handler.
func NewActivityController(activity *activitysvc.Service) *ActivityController {
	if activity == nil {
		panic("dashboard account activity controller: activity service is required")
	}
	return &ActivityController{activity: activity}
}

// Index godoc
// @Summary      Account activity
// @Description  Newest first. Owner, admin and auditor may read. User receives 403. Metadata never includes a secret value.
// @Tags         Account Activity
// @Security     BearerAuth
// @Produce      json
// @Param        accountId  path   string  true   "Account UUID"
// @Param        limit      query  int     false  "Page size"
// @Param        offset     query  int     false  "Rows to skip"
// @Success      200  {object}  map[string]any
// @Failure      401  {object}  responses.ErrorBody
// @Failure      403  {object}  responses.ErrorBody
// @Router       /accounts/{accountId}/activity [get]
func (ctrl *ActivityController) Index(ctx http.Context) http.Response {
	account, role, errResp := accountCaller(ctx)
	if errResp != nil {
		return errResp
	}
	limit, offset := pagination.ParseParams(ctx, activityPageSize)
	rows, total, err := ctrl.activity.List(ctx.Context(), account.ID, role, limit, offset)
	if errResp := mapActivityError(ctx, err); errResp != nil {
		return errResp
	}
	return ctx.Response().Json(http.StatusOK, pagination.Response(rows, total, limit, offset))
}

func accountCaller(ctx http.Context) (*models.Account, string, http.Response) {
	account, _ := requestctx.Account(ctx)
	if account == nil {
		return nil, "", responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "internal_error"})
	}
	role, _ := requestctx.AccountRole(ctx)
	return account, role, nil
}

func mapActivityError(ctx http.Context, err error) http.Response {
	if err == nil {
		return nil
	}
	if errors.Is(err, activitysvc.ErrReadForbidden) {
		return responses.Send(ctx, http.StatusForbidden, http.Json{"error": err.Error()})
	}
	return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "internal_error"})
}
