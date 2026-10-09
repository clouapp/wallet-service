package users

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	usersrequests "github.com/macrowallets/waas/app/http/requests/dashboard/users"
	resources "github.com/macrowallets/waas/app/http/resources/dashboard/users"
	featuressvc "github.com/macrowallets/waas/app/services/features"
	usersvc "github.com/macrowallets/waas/app/services/users"
)

// ProfileController reads and changes the signed-in user's profile.
type ProfileController struct {
	users    *usersvc.Service
	features *featuressvc.Service
}

// NewProfileController wires the profile handlers: the user service writes the
// profile, the features service lists the globally active flags.
func NewProfileController(users *usersvc.Service, features *featuressvc.Service) *ProfileController {
	if users == nil {
		panic("dashboard profile controller: users service is required")
	}
	if features == nil {
		panic("dashboard profile controller: feature flags are required")
	}
	return &ProfileController{users: users, features: features}
}

// Show godoc
//
//	@Summary		Get current user profile
//	@Description	Returns the authenticated user's profile
//	@Tags			User
//	@Security		BearerAuth
//	@Produce		json
//	@Success		200	{object}	resources.MeProfile
//	@Failure		401	{object}	responses.ErrorBody
//	@Router			/users/me [get]
func (c *ProfileController) Show(ctx http.Context) http.Response {
	user := requestctx.MustUser(ctx)

	features, err := c.features.ActiveGlobal(ctx.Context())
	if err != nil {
		return mapError(ctx, err, "internal_error")
	}

	return ctx.Response().Success().Json(resources.NewMeProfile(user, features))
}

// Update godoc
//
//	@Summary		Update current user profile
//	@Description	Updates the authenticated user's full name
//	@Tags			User
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			request	body		UpdateMeSwagger	true	"Update payload"
//	@Success		200		{object}	resources.User
//	@Failure		400		{object}	responses.ErrorBody
//	@Failure		401		{object}	responses.ErrorBody
//	@Router			/users/me [patch]
func (c *ProfileController) Update(ctx http.Context) http.Response {
	user := requestctx.MustUser(ctx)

	var req usersrequests.UpdateMeRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}

	updated, err := c.users.UpdateProfile(ctx.Context(), user, usersvc.ProfileInput{FullName: req.FullName})
	if err != nil {
		return mapError(ctx, err, "failed to update profile")
	}

	return ctx.Response().Success().Json(resources.UserFrom(updated))
}
