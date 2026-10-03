package accounts

import (
	"errors"
	"os"
	"strings"

	"github.com/goravel/framework/contracts/http"

	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	accountsvc "github.com/macrowallets/waas/app/services/account"
	usersvc "github.com/macrowallets/waas/app/services/users"
)

// InvitesController previews and accepts account invites. The raw token stays
// in the link; handlers never log it.
type InvitesController struct {
	accounts *accountsvc.Service
	users    *usersvc.Service
}

// NewInvitesController wires invite preview and accept.
func NewInvitesController(accounts *accountsvc.Service, users *usersvc.Service) *InvitesController {
	if accounts == nil {
		panic("dashboard invites controller: account service is required")
	}
	if users == nil {
		panic("dashboard invites controller: user service is required")
	}
	return &InvitesController{accounts: accounts, users: users}
}

func frontendBaseURL() string {
	if value := strings.TrimSpace(os.Getenv("APP_FRONTEND_URL")); value != "" {
		return value
	}
	if value := strings.TrimSpace(appfacades.Config().GetString("app.url")); value != "" {
		return value
	}
	return "http://localhost:2001"
}

// Preview reports the public facts of a pending invite.
func (ctrl *InvitesController) Preview(ctx http.Context) http.Response {
	token, err := requests.RouteString(ctx, "token")
	if err != nil {
		return responses.Send(ctx, http.StatusNotFound, http.Json{"error": "invite is invalid or expired"})
	}
	invite, needsPassword, err := ctrl.accounts.PreviewInvite(ctx.Context(), token)
	if err != nil || invite == nil {
		return responses.Send(ctx, http.StatusNotFound, http.Json{"error": "invite is invalid or expired"})
	}
	accountName := ""
	if account, accErr := ctrl.accounts.FindByID(ctx.Context(), invite.AccountID); accErr == nil && account != nil {
		accountName = account.Name
	}
	inviter := ""
	if invite.InvitedBy != nil {
		if user, userErr := ctrl.users.FindByID(ctx.Context(), *invite.InvitedBy); userErr == nil && user != nil {
			inviter = user.FullName
			if inviter == "" {
				inviter = user.Email
			}
		}
	}
	return responses.Send(ctx, http.StatusOK, http.Json{
		"account_name":   accountName,
		"inviter":        inviter,
		"role":           invite.Role,
		"email":          invite.Email,
		"needs_password": needsPassword,
	})
}

// Accept spends an invite token.
func (ctrl *InvitesController) Accept(ctx http.Context) http.Response {
	var req requests.AcceptInviteRequest
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}
	sessionUser, sessionErr := optionalSessionUser(ctx)
	if sessionErr != nil {
		return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "invalid token"})
	}
	user, err := ctrl.accounts.AcceptInvite(ctx.Context(), req.Token, req.Password, req.FullName, sessionUser)
	if err != nil {
		switch {
		case errors.Is(err, accountsvc.ErrInviteInvalid):
			return responses.Send(ctx, http.StatusNotFound, http.Json{"error": err.Error()})
		case errors.Is(err, accountsvc.ErrInviteLoginRequired):
			return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": err.Error()})
		case errors.Is(err, accountsvc.ErrInvitePassword), errors.Is(err, accountsvc.ErrGrantRole):
			return responses.Send(ctx, http.StatusUnprocessableEntity, http.Json{"error": err.Error()})
		default:
			return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to accept invite"})
		}
	}
	return responses.Send(ctx, http.StatusOK, http.Json{
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
	guard := appfacades.Auth(ctx)
	if _, err := guard.Parse(strings.TrimPrefix(bearer, "Bearer ")); err != nil {
		return nil, err
	}
	var user models.User
	if err := guard.User(&user); err != nil {
		return nil, err
	}
	return &user, nil
}
