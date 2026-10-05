package wallets

import (
	"errors"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	walletusers "github.com/macrowallets/waas/app/http/resources/dashboard/wallets/users"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/policies"
	accountsvc "github.com/macrowallets/waas/app/services/account"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

// UsersController serves the dashboard wallet membership routes.
type UsersController struct {
	members     *walletrecords.Members
	accounts    *accountsvc.Service
	memberships *walletrecords.Memberships
}

// WalletUsersControllerDeps is everything the dashboard wallet users controller needs.
// Every field is required.
type WalletUsersControllerDeps struct {
	Members     *walletrecords.Members
	Accounts    *accountsvc.Service
	Memberships *walletrecords.Memberships
}

// NewUsersController wires the dashboard wallet user handlers from WalletUsersControllerDeps.
func NewUsersController(deps WalletUsersControllerDeps) *UsersController {
	if deps.Members == nil {
		panic("dashboard wallet users controller: wallet users service is required")
	}
	if deps.Accounts == nil {
		panic("dashboard wallet users controller: account service is required")
	}
	if deps.Memberships == nil {
		panic("dashboard wallet users controller: wallet memberships are required")
	}
	return &UsersController{
		members:     deps.Members,
		accounts:    deps.Accounts,
		memberships: deps.Memberships,
	}
}

// ListWalletUsers godoc
// @Summary      List wallet users
// @Description  Returns all active members of a wallet
// @Tags         Wallet Users
// @Security     BearerAuth
// @Produce      json
// @Param        walletId  path  string  true  "Wallet UUID"
// @Success      200  {object}  WalletUserListResponse
// @Failure      403  {object}  ErrorResponse
// @Failure      404  {object}  ErrorResponse
// @Router       /wallets/{walletId}/users [get]
func (ctrl *UsersController) ListWalletUsers(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)

	members, err := ctrl.members.FindByWalletID(ctx.Context(), wallet.ID)
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to fetch wallet users"})
	}
	return responses.Send(ctx, http.StatusOK, http.Json{"data": walletusers.WalletUsersFrom(members)})
}

// AddWalletUser godoc
// @Summary      Add a user to a wallet
// @Description  Adds a user to the wallet with the specified role. Requires wallet or account owner/admin.
// @Tags         Wallet Users
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        walletId  path      string              true  "Wallet UUID"
// @Param        request   body      AddWalletUserSwagger  true  "User and role payload"
// @Success      201  {object}  walletusers.WalletUser
// @Failure      400  {object}  ErrorResponse
// @Failure      403  {object}  ErrorResponse
// @Router       /wallets/{walletId}/users [post]
func (ctrl *UsersController) AddWalletUser(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)

	var req requests.AddWalletUserRequest
	if resp := validateRequest(ctx, &req); resp != nil {
		return resp
	}
	targetID, _ := uuid.Parse(req.UserID)
	roles, err := models.ParseWalletRoles(req.Roles)
	if err != nil {
		return responses.Send(ctx, http.StatusUnprocessableEntity, http.Json{"error": err.Error()})
	}
	roleList := models.FormatWalletRoles(roles)
	if resp := ctrl.requireActiveAccountMember(ctx, wallet, targetID); resp != nil {
		return resp
	}

	existing, existErr := ctrl.members.FindByWalletAndUserIncludeDeleted(ctx.Context(), wallet.ID, targetID)
	if existErr != nil && !errors.Is(existErr, models.ErrRepositoryNotFound) {
		facades.Log().WithContext(ctx).Errorf("wallet-users: lookup existing: %v", existErr)
	}
	if existing != nil && existing.DeletedAt != nil {
		if err := ctrl.members.Restore(ctx.Context(), existing.ID); err != nil {
			return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to restore wallet user"})
		}
		if err := ctrl.members.SetRoles(ctx.Context(), existing.ID, roleList); err != nil {
			facades.Log().WithContext(ctx).Errorf("wallet-users: update roles: %v", err)
		} else {
			existing.Roles = roleList
		}
		return responses.Send(ctx, http.StatusCreated, walletusers.WalletUserPtr(existing))
	}

	wu := &models.WalletUser{
		ID:       uuid.New(),
		WalletID: wallet.ID,
		UserID:   targetID,
		Roles:    roleList,
		Status:   "active",
	}
	if err := ctrl.members.Create(ctx.Context(), wu); err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to add wallet user"})
	}
	return responses.Send(ctx, http.StatusCreated, walletusers.WalletUserPtr(wu))
}

// requireActiveAccountMember rejects a user_id that is not an active member of
// the wallet's account. An unknown user gets the same 422.
func (ctrl *UsersController) requireActiveAccountMember(ctx http.Context, wallet *models.Wallet, userID uuid.UUID) http.Response {
	if wallet.AccountID == nil {
		return responses.Send(ctx, http.StatusUnprocessableEntity, http.Json{"error": "user is not an active member of this account"})
	}
	member, err := ctrl.accounts.FindMember(ctx.Context(), *wallet.AccountID, userID)
	if err != nil {
		if errors.Is(err, models.ErrRepositoryNotFound) {
			return responses.Send(ctx, http.StatusUnprocessableEntity, http.Json{"error": "user is not an active member of this account"})
		}
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to add wallet user"})
	}
	if member == nil || member.Status != "active" {
		return responses.Send(ctx, http.StatusUnprocessableEntity, http.Json{"error": "user is not an active member of this account"})
	}
	return nil
}

// RemoveWalletUser godoc
// @Summary      Remove a user from a wallet
// @Description  Soft-deletes the wallet_user membership. Requires wallet or account owner/admin.
// @Tags         Wallet Users
// @Security     BearerAuth
// @Produce      json
// @Param        walletId  path  string  true  "Wallet UUID"
// @Param        userId    path  string  true  "User UUID to remove"
// @Success      204  "No content"
// @Failure      403  {object}  ErrorResponse
// @Failure      404  {object}  ErrorResponse
// @Router       /wallets/{walletId}/users/{userId} [delete]
func (ctrl *UsersController) RemoveWalletUser(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)
	if resp := controllers.Deny(ctx, policies.WalletRemoveUser(controllers.WalletMembership(ctx, ctrl.memberships, wallet.ID))); resp != nil {
		return resp
	}

	targetID, err := requests.RouteUUID(ctx, "userId")
	if err != nil {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "invalid user id"})
	}

	if err := ctrl.members.SoftDelete(ctx.Context(), wallet.ID, targetID); err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to remove wallet user"})
	}
	return ctx.Response().NoContent()
}

// ---- Request/Response types ----

type AddWalletUserSwagger struct {
	UserID string `json:"user_id" example:"00000000-0000-0000-0000-000000000001"`
	Roles  string `json:"roles" example:"viewer"`
}

type WalletUserListResponse struct {
	Data []walletusers.WalletUser `json:"data"`
}
