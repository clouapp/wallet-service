package accounts

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/requests"
	accountsrequests "github.com/macrowallets/waas/app/http/requests/dashboard/accounts"
	resources "github.com/macrowallets/waas/app/http/resources/dashboard/accounts"
	"github.com/macrowallets/waas/app/http/responses"
	accountsvc "github.com/macrowallets/waas/app/services/account"
)

// InviteController lists, issues, resends and revokes the invites of the
// account in scope, and previews and accepts an invite by its token on the
// guest auth routes. The raw token stays in the link; nothing here logs it.
type InviteController struct {
	accounts *accountsvc.Service
}

// NewInviteController wires the invite handlers to the account service, which
// also dispatches the invite mail.
func NewInviteController(accounts *accountsvc.Service) *InviteController {
	if accounts == nil {
		panic("dashboard invite controller: account service is required")
	}
	return &InviteController{accounts: accounts}
}

// Index lists the account's invites for a caller who holds users.read.
func (c *InviteController) Index(ctx http.Context) http.Response {
	account := requestctx.MustAccount(ctx)

	limit, offset := pagination.ParseParams(ctx, 20)

	invites, total, err := c.accounts.ListInvites(ctx.Context(), account.ID, limit, offset)
	if err != nil {
		return mapError(ctx, err, "failed to fetch invites")
	}

	return ctx.Response().Success().Json(pagination.Response(resources.NewInvites(invites), total, limit, offset))
}

// Store issues an invite and answers 202. An email that already has a user
// gets the same answer and the same fields. The body is the list view: no
// token, hash or link, and nothing that says whether the email has a user.
// users.write is the route middleware; MayGrant stays in the account service.
func (c *InviteController) Store(ctx http.Context) http.Response {
	account := requestctx.MustAccount(ctx)
	callerID := requestctx.MustUserID(ctx)

	var req accountsrequests.AddAccountUserRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}

	issued, err := c.accounts.InviteMember(ctx.Context(), accountsvc.InviteInput{
		AccountID: account.ID,
		InvitedBy: callerID,
		Email:     req.Email,
		Role:      req.Role,
	})
	if err != nil {
		return mapInviteError(ctx, err, "failed to create invite")
	}

	return ctx.Response().Status(http.StatusAccepted).Json(resources.NewInvite(issued.Invite))
}

// Resend rotates the invite token and mails the new link. users.write is the
// route middleware. The answer matches Store: 202 and the list view.
func (c *InviteController) Resend(ctx http.Context) http.Response {
	account := requestctx.MustAccount(ctx)

	inviteID, err := requests.RouteUUID(ctx, "id")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid invite id")
	}

	issued, err := c.accounts.ResendInviteLink(ctx.Context(), account.ID, inviteID)
	if err != nil {
		return mapError(ctx, err, "failed to resend invite")
	}

	return ctx.Response().Status(http.StatusAccepted).Json(resources.NewInvite(issued.Invite))
}

// Destroy revokes one open invite. users.write is the route middleware. The
// answer is empty and no mail is sent.
func (c *InviteController) Destroy(ctx http.Context) http.Response {
	account := requestctx.MustAccount(ctx)

	inviteID, err := requests.RouteUUID(ctx, "id")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid invite id")
	}

	if err := c.accounts.RevokeInvite(ctx.Context(), account.ID, inviteID); err != nil {
		return mapError(ctx, err, "failed to revoke invite")
	}

	return ctx.Response().NoContent()
}

// Preview reports the public facts of a pending invite to anyone holding its
// token. Any failure is the same 404, so the answer tells nothing more.
func (c *InviteController) Preview(ctx http.Context) http.Response {
	token, err := requests.RouteString(ctx, "token")
	if err != nil {
		return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "invite is invalid or expired")
	}

	preview, err := c.accounts.PreviewInvite(ctx.Context(), token)
	if err != nil {
		return mapPreviewError(ctx, err)
	}

	return ctx.Response().Success().Json(resources.NewInvitePreview(preview))
}

// Accept spends an invite token. A new email gets a user; an email that
// already has one must be the signed-in caller.
func (c *InviteController) Accept(ctx http.Context) http.Response {
	var req accountsrequests.AcceptInviteRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}
	caller, err := middleware.OptionalSessionUser(ctx)
	if err != nil {
		return responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "invalid token")
	}

	user, err := c.accounts.AcceptInvite(ctx.Context(), req.Token, req.Password, req.FullName, caller)
	if err != nil {
		return mapAcceptError(ctx, err)
	}

	return ctx.Response().Success().Json(resources.NewAcceptedInvite(user))
}
