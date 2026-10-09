package accounts

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/requests"
	accountsrequests "github.com/macrowallets/waas/app/http/requests/dashboard/accounts"
	resources "github.com/macrowallets/waas/app/http/resources/dashboard/accounts"
	"github.com/macrowallets/waas/app/http/responses"
	accountsvc "github.com/macrowallets/waas/app/services/account"
)

// MemberController lists, adds, changes and removes the members of the
// account in scope. users.read and users.write are route middleware; the rank
// rules stay in the account service.
type MemberController struct {
	accounts *accountsvc.Service
}

// NewMemberController wires the member handlers to the account service.
func NewMemberController(accounts *accountsvc.Service) *MemberController {
	if accounts == nil {
		panic("dashboard member controller: account service is required")
	}
	return &MemberController{accounts: accounts}
}

// Index godoc
//
//	@Summary		List account members
//	@Description	Returns all active members of the account with their roles
//	@Tags			Accounts
//	@Security		BearerAuth
//	@Produce		json
//	@Param			accountId	path		string	true	"Account UUID"
//	@Success		200			{object}	AccountUserListResponse
//	@Failure		403			{object}	responses.ErrorBody
//	@Router			/accounts/{accountId}/users [get]
func (c *MemberController) Index(ctx http.Context) http.Response {
	account := requestctx.MustAccount(ctx)

	limit, offset := pagination.ParseParams(ctx, 20)

	members, total, err := c.accounts.ListMembers(ctx.Context(), account.ID, limit, offset)
	if err != nil {
		return mapError(ctx, err, "failed to fetch members")
	}

	return ctx.Response().Success().Json(pagination.Response(resources.AccountUsersFrom(members), total, limit, offset))
}

// Store godoc
//
//	@Summary		Add a user to an account
//	@Description	Adds a user to the account with the specified role. Requires owner or admin.
//	@Tags			Accounts
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			accountId	path		string					true	"Account UUID"
//	@Param			request		body		AddAccountUserSwagger	true	"User and role payload"
//	@Success		201			{object}	resources.AccountUser
//	@Failure		400			{object}	responses.ErrorBody
//	@Failure		403			{object}	responses.ErrorBody
//	@Router			/accounts/{accountId}/users [post]
func (c *MemberController) Store(ctx http.Context) http.Response {
	account := requestctx.MustAccount(ctx)
	callerID := requestctx.MustUserID(ctx)

	var req accountsrequests.AddAccountUserRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}

	added, err := c.accounts.AddMember(ctx.Context(), accountsvc.AddMemberInput{
		AccountID: account.ID,
		ActorID:   callerID,
		Email:     req.Email,
		Role:      req.Role,
	})
	if err != nil {
		return mapInviteError(ctx, err, "failed to create invite")
	}

	if added.Invite != nil {
		return ctx.Response().Status(http.StatusAccepted).Json(resources.NewInviteLink(added.Invite))
	}
	return ctx.Response().Status(http.StatusCreated).Json(resources.AccountUserPtr(added.Member))
}

// Update godoc
//
//	@Summary		Update an account member
//	@Description	Changes role and/or status. Owner and admin only. Suspending leaves the API tokens that member minted for this account.
//	@Tags			Accounts
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			accountId	path		string						true	"Account UUID"
//	@Param			userId		path		string						true	"User UUID"
//	@Param			request		body		UpdateAccountUserSwagger	true	"Role and/or status"
//	@Success		200			{object}	resources.AccountUser
//	@Failure		403			{object}	responses.ErrorBody
//	@Failure		404			{object}	responses.ErrorBody
//	@Failure		422			{object}	responses.ErrorBody
//	@Router			/accounts/{accountId}/users/{userId} [patch]
func (c *MemberController) Update(ctx http.Context) http.Response {
	account := requestctx.MustAccount(ctx)
	callerID := requestctx.MustUserID(ctx)

	targetID, err := requests.RouteUUID(ctx, "userId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid user id")
	}
	var req accountsrequests.UpdateAccountUserRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}

	member, err := c.accounts.UpdateMember(ctx.Context(), accountsvc.UpdateMemberInput{
		AccountID: account.ID,
		ActorID:   callerID,
		TargetID:  targetID,
		Role:      req.Role,
		Status:    req.Status,
	})
	if err != nil {
		return mapError(ctx, err, "internal_error")
	}

	return ctx.Response().Success().Json(resources.AccountUserPtr(member))
}

// Destroy godoc
//
//	@Summary		Remove a user from an account
//	@Description	Soft-deletes the account_user membership. Requires owner or admin.
//	@Tags			Accounts
//	@Security		BearerAuth
//	@Produce		json
//	@Param			accountId	path	string	true	"Account UUID"
//	@Param			userId		path	string	true	"User UUID to remove"
//	@Success		204			"No content"
//	@Failure		403			{object}	responses.ErrorBody
//	@Failure		404			{object}	responses.ErrorBody
//	@Router			/accounts/{accountId}/users/{userId} [delete]
func (c *MemberController) Destroy(ctx http.Context) http.Response {
	account := requestctx.MustAccount(ctx)
	callerID := requestctx.MustUserID(ctx)

	targetID, err := requests.RouteUUID(ctx, "userId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid user id")
	}

	if err := c.accounts.RemoveMember(ctx.Context(), account.ID, callerID, targetID); err != nil {
		return mapError(ctx, err, "internal_error")
	}

	return ctx.Response().NoContent()
}
