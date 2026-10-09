package users

import (
	"strings"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	usersrequests "github.com/macrowallets/waas/app/http/requests/dashboard/users"
	userresource "github.com/macrowallets/waas/app/http/resources/dashboard/users"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	featuressvc "github.com/macrowallets/waas/app/services/features"
	"github.com/macrowallets/waas/app/services/sessions"
	"github.com/macrowallets/waas/app/services/settings"
	usersvc "github.com/macrowallets/waas/app/services/users"
)

type UsersController struct {
	users        *usersvc.Service
	passwords    *authsvc.Service
	refresh      *sessions.RefreshTokens
	secondFactor *authsvc.SecondFactorVerifier
	revoker      *authsvc.SessionRevoker
	features     *featuressvc.Service
}

// UsersControllerDeps is everything the dashboard users controller needs.
// Every field is required.
type UsersControllerDeps struct {
	Users        *usersvc.Service
	Passwords    *authsvc.Service
	Refresh      *sessions.RefreshTokens
	SecondFactor *authsvc.SecondFactorVerifier
	Revoker      *authsvc.SessionRevoker
	Features     *featuressvc.Service
}

// NewUsersController wires the dashboard user handlers from UsersControllerDeps.
// Services are the provider singletons, resolved once at boot.
func NewUsersController(deps UsersControllerDeps) *UsersController {
	if deps.Users == nil {
		panic("dashboard users controller: users service is required")
	}
	if deps.Passwords == nil {
		panic("dashboard users controller: auth service is required")
	}
	if deps.Refresh == nil {
		panic("dashboard users controller: refresh tokens are required")
	}
	if deps.SecondFactor == nil {
		panic("dashboard users controller: second factor verifier is required")
	}
	if deps.Revoker == nil {
		panic("dashboard users controller: session revoker is required")
	}
	if deps.Features == nil {
		panic("dashboard users controller: feature flags are required")
	}
	return &UsersController{
		users:        deps.Users,
		passwords:    deps.Passwords,
		refresh:      deps.Refresh,
		secondFactor: deps.SecondFactor,
		revoker:      deps.Revoker,
		features:     deps.Features,
	}
}

func (ctrl *UsersController) sessions() controllers.SessionIssuer {
	return controllers.SessionIssuer{Passwords: ctrl.passwords, Refresh: ctrl.refresh, Revoker: ctrl.revoker}
}

// GetMe godoc
// @Summary      Get current user profile
// @Description  Returns the authenticated user's profile
// @Tags         User
// @Security     BearerAuth
// @Produce      json
// @Success      200  {object}  MeProfile
// @Failure      401  {object}  responses.ErrorBody
// @Router       /users/me [get]
func (ctrl *UsersController) GetMe(ctx http.Context) http.Response {
	user := requestctx.MustUser(ctx)
	if user == nil {
		return responses.Fail(ctx, http.StatusUnauthorized, "unauthenticated", "unauthenticated")
	}
	names, err := ctrl.features.ActiveGlobal(ctx.Context())
	if err != nil {
		appfacades.Log().WithContext(ctx).Errorf("user: active features: %v", err)
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternalError, "internal_error")
	}
	return ctx.Response().Success().Json(MeProfile{User: *userresource.UserFrom(user), Features: names})
}

// MeProfile is GET /v1/users/me. User fields stay as they are. Features is
// the globally active flag keys in catalog order. A missing global row uses
// the catalog default. Account rows are not included. Login and PATCH
// /v1/users/me do not carry this field.
type MeProfile struct {
	userresource.User
	Features []string `json:"features"`
}

// UpdateMe godoc
// @Summary      Update current user profile
// @Description  Updates the authenticated user's full name
// @Tags         User
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        request  body      UpdateMeSwagger  true  "Update payload"
// @Success      200      {object}  userresource.User
// @Failure      400      {object}  responses.ErrorBody
// @Failure      401      {object}  responses.ErrorBody
// @Router       /users/me [patch]
func (ctrl *UsersController) UpdateMe(ctx http.Context) http.Response {
	user := requestctx.MustUser(ctx)

	var req usersrequests.UpdateMeRequest
	if errResp := requests.Validate(ctx, &req); errResp != nil {
		return errResp
	}

	if req.FullName != "" {
		if err := ctrl.users.UpdateFullName(ctx.Context(), user.ID, req.FullName); err != nil {
			return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to update profile")
		}
		user.FullName = req.FullName
	}

	return ctx.Response().Success().Json(userresource.UserFrom(user))
}

// ChangePassword godoc
// @Summary      Change password
// @Description  Validates the current password and updates it
// @Tags         User
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        request  body      ChangePasswordSwagger  true  "Password change payload"
// @Success      200      {object}  map[string]string
// @Failure      400      {object}  responses.ErrorBody
// @Failure      401      {object}  responses.ErrorBody
// @Router       /users/me/password [post]
func (ctrl *UsersController) ChangePassword(ctx http.Context) http.Response {
	user := requestctx.MustUser(ctx)

	var req usersrequests.ChangePasswordRequest
	if errResp := requests.Validate(ctx, &req); errResp != nil {
		return errResp
	}

	if !ctrl.passwords.CheckPassword(req.CurrentPassword, user.PasswordHash) {
		return responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "current password is incorrect")
	}

	hash, err := ctrl.passwords.HashPassword(req.NewPassword)
	if err != nil {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to hash password")
	}

	if err := ctrl.users.UpdatePasswordHash(ctx.Context(), user.ID, hash); err != nil {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to update password")
	}

	session, err := ctrl.sessions().ReplaceSessions(ctx, user.ID)
	if err != nil {
		appfacades.Log().WithContext(ctx).Errorf("auth: change password: replace sessions: %v", err)
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "password updated but sessions could not be renewed")
	}
	return ctx.Response().Success().Json(http.Json{
		"message":       "password updated successfully",
		"access_token":  session.AccessToken,
		"refresh_token": session.RefreshToken,
	})
}

// SetupTOTP godoc
// @Summary      Begin TOTP enrollment
// @Description  Generates a TOTP secret and QR URL; stores encrypted secret until verified
// @Tags         User
// @Security     BearerAuth
// @Produce      json
// @Success      200  {object}  TotpSetupSwagger
// @Failure      500  {object}  responses.ErrorBody
// @Router       /users/me/totp/setup [post]
func (ctrl *UsersController) SetupTOTP(ctx http.Context) http.Response {
	user := requestctx.MustUser(ctx)
	if user.TotpEnabled {
		return responses.Fail(ctx, http.StatusConflict, responses.CodeConflict, "2FA is already enabled")
	}

	secret, qrURL, err := ctrl.passwords.GenerateTOTP(user.Email)
	if err != nil {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to generate TOTP secret")
	}

	sealed, err := settings.Seal(appfacades.Crypt(), secret)
	if err != nil {
		appfacades.Log().WithContext(ctx).Errorf("user: setup totp: seal failed")
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to encrypt secret")
	}

	if err := ctrl.users.UpdateTotpSecret(ctx.Context(), user.ID, sealed); err != nil {
		appfacades.Log().WithContext(ctx).Errorf("user: setup totp: save failed")
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to save TOTP secret")
	}

	return ctx.Response().Success().Json(http.Json{
		"secret": secret,
		"qr_url": qrURL,
	})
}

// ConfirmTOTP godoc
// @Summary      Complete TOTP enrollment
// @Description  Verifies the TOTP code, enables 2FA, and returns one-time recovery codes
// @Tags         User
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        request  body      ConfirmTotpSwagger  true  "TOTP verification code"
// @Success      200      {object}  map[string]interface{}
// @Failure      400      {object}  responses.ErrorBody
// @Failure      401      {object}  responses.ErrorBody
// @Failure      500      {object}  responses.ErrorBody
// @Router       /users/me/totp/verify [post]
func (ctrl *UsersController) ConfirmTOTP(ctx http.Context) http.Response {
	user := requestctx.MustUser(ctx)

	var req usersrequests.ConfirmTwoFactorRequest
	if errResp := requests.Validate(ctx, &req); errResp != nil {
		return errResp
	}

	decryptedSecret, err := ctrl.secondFactor.OpenSecret(user.ID)
	if err != nil {
		appfacades.Log().WithContext(ctx).Errorf("user: confirm totp: open secret failed")
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to decrypt secret")
	}
	if decryptedSecret == "" {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "no TOTP secret found — call setup first")
	}

	matched, err := ctrl.secondFactor.RecordConfirmedCode(user.ID, decryptedSecret, req.Code)
	if err != nil {
		appfacades.Log().WithContext(ctx).Errorf("user: confirm totp: %v", err)
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to enable 2FA")
	}
	if !matched {
		return responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "invalid verification code")
	}

	if err := ctrl.users.EnableTotp(ctx.Context(), user.ID); err != nil {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to enable 2FA")
	}

	codes, hashes, err := ctrl.passwords.GenerateRecoveryCodes()
	if err != nil {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to generate recovery codes")
	}

	_ = ctrl.users.DeleteRecoveryCodes(ctx.Context(), user.ID)

	var recoveryCodes []models.TotpRecoveryCode
	for _, h := range hashes {
		recoveryCodes = append(recoveryCodes, models.TotpRecoveryCode{
			ID:       uuid.New(),
			UserID:   user.ID,
			CodeHash: h,
		})
	}
	_ = ctrl.users.CreateRecoveryCodes(ctx.Context(), recoveryCodes)

	user.TotpEnabled = true
	user.TotpSecret = ""
	resp := map[string]interface{}{
		"user":           userresource.UserFrom(user),
		"recovery_codes": codes,
	}
	return ctx.Response().Success().Json(resp)
}

// DisableTOTP godoc
// @Summary      Disable TOTP
// @Description  Disables 2FA and clears TOTP secret and recovery codes
// @Tags         User
// @Security     BearerAuth
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Failure      500  {object}  responses.ErrorBody
// @Router       /users/me/totp [delete]
func (ctrl *UsersController) DisableTOTP(ctx http.Context) http.Response {
	sessionUser := requestctx.MustUser(ctx)
	user, err := ctrl.users.FindByID(ctx.Context(), sessionUser.ID)
	if err != nil || user == nil {
		return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "user not found")
	}
	if user.TotpEnabled {
		if resp := ctrl.requireLiveSecondFactor(ctx, user); resp != nil {
			return resp
		}
	}

	if err := ctrl.users.DisableTotp(ctx.Context(), user.ID); err != nil {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to disable 2FA")
	}
	_ = ctrl.users.DeleteRecoveryCodes(ctx.Context(), user.ID)

	session, err := ctrl.sessions().ReplaceSessions(ctx, user.ID)
	if err != nil {
		appfacades.Log().WithContext(ctx).Errorf("auth: disable totp: replace sessions: %v", err)
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "2FA disabled but sessions could not be renewed")
	}
	user.TotpEnabled = false
	user.TotpSecret = ""
	return ctx.Response().Success().Json(http.Json{
		"user":          userresource.UserFrom(user),
		"access_token":  session.AccessToken,
		"refresh_token": session.RefreshToken,
	})
}

func (ctrl *UsersController) requireLiveSecondFactor(ctx http.Context, user *models.User) http.Response {
	var req usersrequests.DisableTotpRequest
	if errResp := requests.Validate(ctx, &req); errResp != nil {
		return errResp
	}
	if strings.TrimSpace(req.Code) == "" && strings.TrimSpace(req.RecoveryCode) == "" {
		return controllers.TwoFactorErrorResponse(ctx, authsvc.ErrInvalidSecondFactor)
	}
	if err := ctrl.secondFactor.Verify(user, strings.TrimSpace(req.Code), strings.TrimSpace(req.RecoveryCode)); err != nil {
		return controllers.TwoFactorErrorResponse(ctx, err)
	}
	return nil
}

// ---- Swagger-only types ----

type UpdateMeSwagger struct {
	FullName string `json:"full_name" example:"Alice Smith"`
}

type ChangePasswordSwagger struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

type TotpSetupSwagger struct {
	Secret string `json:"secret" example:"JBSWY3DPEHPK3PXP"`
	QrURL  string `json:"qr_url" example:"otpauth://totp/..."`
}

type ConfirmTotpSwagger struct {
	Code string `json:"code" example:"123456"`
}
