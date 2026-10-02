package controllers

import (
	"errors"
	"os"
	"strings"

	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/models"
	accountsvc "github.com/macrowallets/waas/app/services/account"
)

func frontendBaseURL() string {
	if value := strings.TrimSpace(os.Getenv("APP_FRONTEND_URL")); value != "" {
		return value
	}
	if value := strings.TrimSpace(facades.Config().GetString("app.url")); value != "" {
		return value
	}
	return "http://localhost:2001"
}

func PreviewInvite(ctx http.Context) http.Response {
	invite, needsPassword, err := accountSvc().PreviewInvite(ctx.Context(), ctx.Request().Route("token"))
	if err != nil || invite == nil {
		return ctx.Response().Json(http.StatusNotFound, http.Json{"error": "invite is invalid or expired"})
	}
	accountName := ""
	if account, accErr := container.Get().AccountRepo.FindByID(invite.AccountID); accErr == nil && account != nil {
		accountName = account.Name
	}
	inviter := ""
	if invite.InvitedBy != nil {
		if user, userErr := container.Get().UserRepo.FindByID(*invite.InvitedBy); userErr == nil && user != nil {
			inviter = user.FullName
			if inviter == "" {
				inviter = user.Email
			}
		}
	}
	return ctx.Response().Json(http.StatusOK, http.Json{
		"account_name":   accountName,
		"inviter":        inviter,
		"role":           invite.Role,
		"email":          invite.Email,
		"needs_password": needsPassword,
	})
}

func AcceptInvite(ctx http.Context) http.Response {
	var req requests.AcceptInviteRequest
	if resp := validateRequest(ctx, &req); resp != nil {
		return resp
	}
	sessionUser, sessionErr := optionalSessionUser(ctx)
	if sessionErr != nil {
		return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "invalid token"})
	}
	user, err := accountSvc().AcceptInvite(ctx.Context(), req.Token, req.Password, req.FullName, sessionUser)
	if err != nil {
		switch {
		case errors.Is(err, accountsvc.ErrInviteInvalid):
			return ctx.Response().Json(http.StatusNotFound, http.Json{"error": err.Error()})
		case errors.Is(err, accountsvc.ErrInviteLoginRequired):
			return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": err.Error()})
		case errors.Is(err, accountsvc.ErrInvitePassword):
			return ctx.Response().Json(http.StatusUnprocessableEntity, http.Json{"error": err.Error()})
		default:
			return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to accept invite"})
		}
	}
	return ctx.Response().Json(http.StatusOK, http.Json{
		"user_id": user.ID,
		"email":   user.Email,
		"status":  user.Status,
	})
}

func optionalSessionUser(ctx http.Context) (*models.User, error) {
	bearer := ctx.Request().Header("Authorization", "")
	if !strings.HasPrefix(bearer, "Bearer ") {
		return nil, nil
	}
	guard := facades.Auth(ctx)
	if _, err := guard.Parse(strings.TrimPrefix(bearer, "Bearer ")); err != nil {
		return nil, err
	}
	var user models.User
	if err := guard.User(&user); err != nil {
		return nil, err
	}
	return &user, nil
}
