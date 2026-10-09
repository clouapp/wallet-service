package users

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	usersrequests "github.com/macrowallets/waas/app/http/requests/dashboard/wallets/users"
	walletusers "github.com/macrowallets/waas/app/http/resources/dashboard/wallets/users"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

// UserController serves the dashboard wallet membership routes.
type UserController struct {
	members     *walletrecords.Members
	memberships *walletrecords.Memberships
}

// NewUserController wires the controller with the wallet members and the
// memberships that add one.
func NewUserController(members *walletrecords.Members, memberships *walletrecords.Memberships) *UserController {
	if members == nil {
		panic("dashboard wallet users controller: wallet users service is required")
	}
	if memberships == nil {
		panic("dashboard wallet users controller: wallet memberships are required")
	}
	return &UserController{members: members, memberships: memberships}
}

// Index godoc
//
//	@Summary		List wallet users
//	@Description	Returns all active members of a wallet
//	@Tags			Wallet Users
//	@Security		BearerAuth
//	@Produce		json
//	@Param			walletId	path		string	true	"Wallet UUID"
//	@Success		200			{object}	WalletUserListResponse
//	@Failure		403			{object}	responses.ErrorBody
//	@Failure		404			{object}	responses.ErrorBody
//	@Router			/wallets/{walletId}/users [get]
func (c *UserController) Index(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)

	members, err := c.members.FindByWalletID(ctx.Context(), wallet.ID)
	if err != nil {
		return mapError(ctx, err, "fetch wallet users")
	}

	return ctx.Response().Success().Json(http.Json{"data": walletusers.WalletUsersFrom(members)})
}

// Store godoc
//
//	@Summary		Add a user to a wallet
//	@Description	Adds a user to the wallet with the specified role. Requires wallet or account owner/admin.
//	@Tags			Wallet Users
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			walletId	path		string					true	"Wallet UUID"
//	@Param			request		body		AddWalletUserSwagger	true	"User and role payload"
//	@Success		201			{object}	walletusers.WalletUser
//	@Failure		400			{object}	responses.ErrorBody
//	@Failure		403			{object}	responses.ErrorBody
//	@Router			/wallets/{walletId}/users [post]
func (c *UserController) Store(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)

	var req usersrequests.StoreRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}

	member, err := c.memberships.AddMember(ctx.Context(), wallet, req.UserUUID(), req.Roles)
	if err != nil {
		return mapError(ctx, err, "add wallet user")
	}

	return ctx.Response().Status(http.StatusCreated).Json(walletusers.WalletUserPtr(member))
}

// Destroy godoc
//
//	@Summary		Remove a user from a wallet
//	@Description	Soft-deletes the wallet_user membership. Requires wallet or account owner/admin.
//	@Tags			Wallet Users
//	@Security		BearerAuth
//	@Produce		json
//	@Param			walletId	path	string	true	"Wallet UUID"
//	@Param			userId		path	string	true	"User UUID to remove"
//	@Success		204			"No content"
//	@Failure		403			{object}	responses.ErrorBody
//	@Failure		404			{object}	responses.ErrorBody
//	@Router			/wallets/{walletId}/users/{userId} [delete]
func (c *UserController) Destroy(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)

	userID, err := requests.RouteUUID(ctx, "userId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid user id")
	}

	if err := c.members.SoftDelete(ctx.Context(), wallet.ID, userID); err != nil {
		return mapError(ctx, err, "remove wallet user")
	}

	return ctx.Response().NoContent()
}
