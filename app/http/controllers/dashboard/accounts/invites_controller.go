package accounts

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/support/carbon"

	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/requests"
	accountsrequests "github.com/macrowallets/waas/app/http/requests/dashboard/accounts"
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

// InvitesControllerDeps is everything the dashboard invites controller needs.
// Accounts issues, resends, and revokes invites. The account service dispatches
// the invite mail. Users accepts an invite. Every field is required.
type InvitesControllerDeps struct {
	Accounts *accountsvc.Service
	Users    *usersvc.Service
}

// NewInvitesController wires invite preview and accept from InvitesControllerDeps.
func NewInvitesController(deps InvitesControllerDeps) *InvitesController {
	if deps.Accounts == nil {
		panic("dashboard invites controller: account service is required")
	}
	if deps.Users == nil {
		panic("dashboard invites controller: user service is required")
	}
	return &InvitesController{
		accounts: deps.Accounts,
		users:    deps.Users,
	}
}

const frontendURLEnv = "APP_FRONTEND_URL"

// frontendBaseURL is the invite link base. It is only APP_FRONTEND_URL.
// A missing value is an error: the host is not taken from app.url or a literal.
func frontendBaseURL() (string, error) {
	return accountsvc.FrontendBase()
}

func requireFrontendBase(ctx http.Context, clientError string) (string, http.Response) {
	base, err := frontendBaseURL()
	if err != nil {
		appfacades.Log().WithContext(ctx).Errorf("account: invite link base is not configured")
		return "", responses.FailMessage(ctx, http.StatusInternalServerError, clientError)
	}
	return base, nil
}

// inviteListItem is one invite a users.read member may see. The token and its
// hash are not fields on this view.
type inviteListItem struct {
	CreatedAt  *carbon.DateTime `json:"created_at"`
	ID         uuid.UUID        `json:"id"`
	AccountID  uuid.UUID        `json:"account_id"`
	Email      string           `json:"email"`
	Role       string           `json:"role"`
	InvitedBy  *uuid.UUID       `json:"invited_by"`
	ExpiresAt  time.Time        `json:"expires_at"`
	AcceptedAt *time.Time       `json:"accepted_at"`
	RevokedAt  *time.Time       `json:"revoked_at"`
}

func inviteListItems(invites []models.AccountInvite) []inviteListItem {
	items := make([]inviteListItem, 0, len(invites))
	for i := range invites {
		invite := invites[i]
		items = append(items, inviteListItem{
			CreatedAt:  invite.CreatedAt,
			ID:         invite.ID,
			AccountID:  invite.AccountID,
			Email:      invite.Email,
			Role:       invite.Role,
			InvitedBy:  invite.InvitedBy,
			ExpiresAt:  invite.ExpiresAt,
			AcceptedAt: invite.AcceptedAt,
			RevokedAt:  invite.RevokedAt,
		})
	}
	return items
}

// Create stores an invite and answers 202. A user that already has this email
// gets the same answer and the same fields. The body is the list view: no
// token, hash, or link, and nothing that says whether the email has an account.
// users.write is the route middleware. MayGrant stays in the account service.
func (ctrl *InvitesController) Create(ctx http.Context) http.Response {
	account := requestctx.MustAccount(ctx)
	callerID := requestctx.MustUserID(ctx)
	var req accountsrequests.AddAccountUserRequest
	if errResp := requests.Validate(ctx, &req); errResp != nil {
		return errResp
	}
	base, errResp := requireFrontendBase(ctx, "failed to create invite")
	if errResp != nil {
		return errResp
	}
	issued, err := ctrl.accounts.IssueInvite(ctx.Context(), account.ID, req.Email, req.Role, callerID, base)
	if err != nil {
		if errors.Is(err, accountsvc.ErrGrantRole) {
			return mapInviteError(ctx, err, "failed to create invite")
		}
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to create invite")
	}
	logInviteMail(ctx, issued)
	return ctx.Response().Status(http.StatusAccepted).Json(inviteCreatedView(issued.Invite))
}

// Resend rotates the invite token. users.write is the route middleware. The
// answer matches create: 202 and the list view, with no token, hash, or link.
func (ctrl *InvitesController) Resend(ctx http.Context) http.Response {
	account := requestctx.MustAccount(ctx)
	inviteID, err := requests.RouteUUID(ctx, "id")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid invite id")
	}
	base, errResp := requireFrontendBase(ctx, "failed to resend invite")
	if errResp != nil {
		return errResp
	}
	issued, err := ctrl.accounts.ResendInvite(ctx.Context(), account.ID, inviteID, base)
	if err != nil {
		if errors.Is(err, accountsvc.ErrInviteInvalid) {
			return responses.Error(ctx, http.StatusNotFound, responses.CodeNotFound, accountsvc.ErrInviteInvalid.Error())
		}
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to resend invite")
	}
	logInviteMail(ctx, issued)
	return ctx.Response().Status(http.StatusAccepted).Json(inviteCreatedView(issued.Invite))
}

// Delete revokes one open invite. users.write is the route middleware. The
// answer is empty: no token, hash, or link. Mail is not sent.
func (ctrl *InvitesController) Delete(ctx http.Context) http.Response {
	account := requestctx.MustAccount(ctx)
	inviteID, err := requests.RouteUUID(ctx, "id")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid invite id")
	}
	if err := ctrl.accounts.RevokeInvite(ctx.Context(), account.ID, inviteID); err != nil {
		if errors.Is(err, accountsvc.ErrInviteInvalid) {
			return responses.Error(ctx, http.StatusNotFound, responses.CodeNotFound, accountsvc.ErrInviteInvalid.Error())
		}
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to revoke invite")
	}
	return ctx.Response().NoContent()
}

func inviteCreatedView(invite *models.AccountInvite) inviteListItem {
	if invite == nil {
		return inviteListItem{}
	}
	view := *invite
	view.TokenHash = ""
	return inviteListItems([]models.AccountInvite{view})[0]
}

// logInviteMail records a dispatch failure without the token, its hash, or the link.
func logInviteMail(ctx http.Context, issued *accountsvc.IssuedInvite) {
	if issued == nil || issued.MailErr == nil {
		return
	}
	appfacades.Log().WithContext(ctx).Errorf("account: send invite mail failed")
}

// List returns the account's invites for a caller who holds users.read.
func (ctrl *InvitesController) List(ctx http.Context) http.Response {
	account := requestctx.MustAccount(ctx)
	limit, offset := pagination.ParseParams(ctx, 20)
	invites, total, err := ctrl.accounts.ListInvites(ctx.Context(), account.ID, limit, offset)
	if err != nil {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to fetch invites")
	}
	return ctx.Response().Success().Json(pagination.Response(inviteListItems(invites), total, limit, offset))
}

// Preview reports the public facts of a pending invite.
func (ctrl *InvitesController) Preview(ctx http.Context) http.Response {
	token, err := requests.RouteString(ctx, "token")
	if err != nil {
		return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "invite is invalid or expired")
	}
	invite, needsPassword, err := ctrl.accounts.PreviewInvite(ctx.Context(), token)
	if err != nil || invite == nil {
		return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "invite is invalid or expired")
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
	return ctx.Response().Success().Json(http.Json{
		"account_name":   accountName,
		"inviter":        inviter,
		"role":           invite.Role,
		"email":          invite.Email,
		"needs_password": needsPassword,
	})
}

// Accept spends an invite token.
func (ctrl *InvitesController) Accept(ctx http.Context) http.Response {
	var req accountsrequests.AcceptInviteRequest
	if errResp := requests.Validate(ctx, &req); errResp != nil {
		return errResp
	}
	sessionUser, sessionErr := optionalSessionUser(ctx)
	if sessionErr != nil {
		return responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "invalid token")
	}
	user, err := ctrl.accounts.AcceptInvite(ctx.Context(), req.Token, req.Password, req.FullName, sessionUser)
	if err != nil {
		switch {
		case errors.Is(err, accountsvc.ErrInviteInvalid):
			return responses.Error(ctx, http.StatusNotFound, responses.CodeNotFound, accountsvc.ErrInviteInvalid.Error())
		case errors.Is(err, accountsvc.ErrInviteLoginRequired):
			return responses.Error(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, accountsvc.ErrInviteLoginRequired.Error())
		case errors.Is(err, accountsvc.ErrInvitePassword):
			return responses.Error(ctx, http.StatusUnprocessableEntity, responses.CodeUnprocessable, accountsvc.ErrInvitePassword.Error())
		case errors.Is(err, accountsvc.ErrGrantRole):
			return responses.Error(ctx, http.StatusUnprocessableEntity, responses.CodeUnprocessable, accountsvc.ErrGrantRole.Error())
		default:
			return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to accept invite")
		}
	}
	return ctx.Response().Success().Json(http.Json{
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
