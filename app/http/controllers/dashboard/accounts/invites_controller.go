package accounts

import (
	"errors"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/support/carbon"

	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/mails"
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
	var req requests.AddAccountUserRequest
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}
	issued, err := ctrl.accounts.IssueInvite(ctx.Context(), account.ID, req.Email, req.Role, callerID, frontendBaseURL())
	if err != nil {
		if errors.Is(err, accountsvc.ErrGrantRole) {
			return responses.Send(ctx, http.StatusForbidden, http.Json{"error": err.Error()})
		}
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to create invite"})
	}
	sendInviteMail(ctx, ctrl.accounts, account, callerID, issued)
	return responses.Send(ctx, http.StatusAccepted, inviteCreatedView(issued.Invite))
}

// Resend rotates the invite token. users.write is the route middleware. The
// answer matches create: 202 and the list view, with no token, hash, or link.
func (ctrl *InvitesController) Resend(ctx http.Context) http.Response {
	account := requestctx.MustAccount(ctx)
	callerID := requestctx.MustUserID(ctx)
	inviteID, err := requests.RouteUUID(ctx, "id")
	if err != nil {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "invalid invite id"})
	}
	issued, err := ctrl.accounts.ResendInvite(ctx.Context(), account.ID, inviteID, frontendBaseURL())
	if err != nil {
		if errors.Is(err, accountsvc.ErrInviteInvalid) {
			return responses.Send(ctx, http.StatusNotFound, http.Json{"error": err.Error()})
		}
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to resend invite"})
	}
	sendInviteMail(ctx, ctrl.accounts, account, callerID, issued)
	return responses.Send(ctx, http.StatusAccepted, inviteCreatedView(issued.Invite))
}

func inviteCreatedView(invite *models.AccountInvite) inviteListItem {
	if invite == nil {
		return inviteListItem{}
	}
	view := *invite
	view.TokenHash = ""
	return inviteListItems([]models.AccountInvite{view})[0]
}

// sendInviteMail delivers the link through the existing mail. That mail does
// not queue, so the token is not placed on a queue. A failure is logged
// without the token, its hash, or the link.
func sendInviteMail(ctx http.Context, accounts *accountsvc.Service, account *models.Account, callerID uuid.UUID, issued *accountsvc.IssuedInvite) {
	if accounts == nil || account == nil || issued == nil || issued.Invite == nil {
		return
	}
	inviterName := "your team"
	if inviter, err := accounts.FindUserByID(ctx.Context(), callerID); err == nil && inviter != nil {
		if inviter.FullName != "" {
			inviterName = inviter.FullName
		} else if inviter.Email != "" {
			inviterName = inviter.Email
		}
	}
	mailErr := appfacades.Mail().To([]string{issued.Invite.Email}).Send(&mails.UserInviteMail{
		To:          issued.Invite.Email,
		InvitedBy:   inviterName,
		AccountName: account.Name,
		InviteLink:  issued.InviteLink,
	})
	if mailErr == nil {
		return
	}
	if inviteSecretInText(mailErr.Error(), issued) {
		appfacades.Log().WithContext(ctx).Errorf("account: send invite mail failed")
		return
	}
	appfacades.Log().WithContext(ctx).Errorf("account: send invite mail failed: %v", mailErr)
}

func inviteSecretInText(text string, issued *accountsvc.IssuedInvite) bool {
	if issued == nil || text == "" {
		return false
	}
	if issued.RawToken != "" && strings.Contains(text, issued.RawToken) {
		return true
	}
	if issued.InviteLink != "" && strings.Contains(text, issued.InviteLink) {
		return true
	}
	if issued.Invite != nil && issued.Invite.TokenHash != "" && strings.Contains(text, issued.Invite.TokenHash) {
		return true
	}
	return false
}

// List returns the account's invites for a caller who holds users.read.
func (ctrl *InvitesController) List(ctx http.Context) http.Response {
	account := requestctx.MustAccount(ctx)
	limit, offset := pagination.ParseParams(ctx, 20)
	invites, total, err := ctrl.accounts.ListInvites(ctx.Context(), account.ID, limit, offset)
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to fetch invites"})
	}
	return responses.Send(ctx, http.StatusOK, pagination.Response(inviteListItems(invites), total, limit, offset))
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
