package controllers

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/models"
	authsvc "github.com/macrowallets/waas/app/services/auth"
)

var userAuthService = authsvc.NewService()

// GetMe godoc
// @Summary      Get current user profile
// @Description  Returns the authenticated user's profile
// @Tags         User
// @Security     BearerAuth
// @Produce      json
// @Success      200  {object}  models.User
// @Failure      401  {object}  ErrorResponse
// @Router       /users/me [get]
func GetMe(ctx http.Context) http.Response {
	user := ctx.Value("user").(*models.User)
	return ctx.Response().Json(http.StatusOK, user)
}

// UpdateMe godoc
// @Summary      Update current user profile
// @Description  Updates the authenticated user's full name
// @Tags         User
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        request  body      UpdateMeSwagger  true  "Update payload"
// @Success      200      {object}  models.User
// @Failure      400      {object}  ErrorResponse
// @Failure      401      {object}  ErrorResponse
// @Router       /users/me [patch]
func UpdateMe(ctx http.Context) http.Response {
	user := ctx.Value("user").(*models.User)

	var req requests.UpdateMeRequest
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}

	if req.FullName != "" {
		if err := container.Get().UserRepo.UpdateFullName(user.ID, req.FullName); err != nil {
			return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to update profile"})
		}
		user.FullName = req.FullName
	}

	return ctx.Response().Json(http.StatusOK, user)
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
// @Failure      400      {object}  ErrorResponse
// @Failure      401      {object}  ErrorResponse
// @Router       /users/me/password [post]
func ChangePassword(ctx http.Context) http.Response {
	user := ctx.Value("user").(*models.User)

	var req requests.ChangePasswordRequest
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}

	if !userAuthService.CheckPassword(req.CurrentPassword, user.PasswordHash) {
		return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "current password is incorrect"})
	}

	hash, err := userAuthService.HashPassword(req.NewPassword)
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to hash password"})
	}

	if err := container.Get().UserRepo.UpdatePasswordHash(user.ID, hash); err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to update password"})
	}

	return ctx.Response().Json(http.StatusOK, http.Json{"message": "password updated successfully"})
}

// ListMyAccounts godoc
// @Summary      List accounts for current user
// @Description  Returns a paginated list of accounts the authenticated user is a member of
// @Tags         User
// @Security     BearerAuth
// @Produce      json
// @Param        limit   query   int  false  "Max results (default 20)"  example(20)
// @Param        offset  query   int  false  "Pagination offset"         example(0)
// @Success      200  {object}  AccountListResponse
// @Failure      401  {object}  ErrorResponse
// @Router       /users/me/accounts [get]
func ListMyAccounts(ctx http.Context) http.Response {
	userID := ctx.Value("user_id").(uuid.UUID)

	limit, offset := pagination.ParseParams(ctx, 20)

	memberships, total, err := container.Get().AccountUserRepo.PaginateByUserID(userID, limit, offset)
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to fetch accounts"})
	}

	accountIDs := make([]uuid.UUID, 0, len(memberships))
	for _, m := range memberships {
		accountIDs = append(accountIDs, m.AccountID)
	}

	accounts, err := container.Get().AccountRepo.FindByIDs(accountIDs)
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to fetch accounts"})
	}

	return ctx.Response().Json(http.StatusOK, pagination.Response(accounts, total, limit, offset))
}

// UpdateDefaultAccount godoc
// @Summary      Set default account
// @Description  Updates the authenticated user's default account
// @Tags         User
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        request  body      UpdateDefaultAccountSwagger  true  "Default account payload"
// @Success      200      {object}  map[string]interface{}
// @Failure      400      {object}  ErrorResponse
// @Failure      403      {object}  ErrorResponse
// @Router       /users/me/default-account [patch]
func UpdateDefaultAccount(ctx http.Context) http.Response {
	userID := ctx.Value("user_id").(uuid.UUID)

	var req requests.UpdateDefaultAccountRequest
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}

	accountID, _ := uuid.Parse(req.AccountID)

	au, err := container.Get().AccountUserRepo.FindByAccountAndUser(accountID, userID)
	if err != nil || au == nil {
		return ctx.Response().Json(http.StatusForbidden, http.Json{"error": "not a member of this account"})
	}

	userPtr, _ := container.Get().UserRepo.FindByID(userID)
	if userPtr == nil {
		return ctx.Response().Json(http.StatusNotFound, http.Json{"error": "user not found"})
	}
	userPtr.DefaultAccountID = &accountID
	if err := container.Get().UserRepo.UpdateDefaultAccountID(userPtr.ID, &accountID); err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to update default account"})
	}

	account, _ := container.Get().AccountRepo.FindByID(accountID)
	return ctx.Response().Json(http.StatusOK, http.Json{"account": account})
}

// SetupTOTP godoc
// @Summary      Begin TOTP enrollment
// @Description  Generates a TOTP secret and QR URL; stores encrypted secret until verified
// @Tags         User
// @Security     BearerAuth
// @Produce      json
// @Success      200  {object}  TotpSetupSwagger
// @Failure      500  {object}  ErrorResponse
// @Router       /users/me/totp/setup [post]
func SetupTOTP(ctx http.Context) http.Response {
	user := ctx.Value("user").(*models.User)

	secret, qrURL, err := userAuthService.GenerateTOTP(user.Email)
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to generate TOTP secret"})
	}

	encryptedSecret, err := facades.Crypt().EncryptString(secret)
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to encrypt secret"})
	}

	if err := container.Get().UserRepo.UpdateTotpSecret(user.ID, encryptedSecret); err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to save TOTP secret"})
	}

	return ctx.Response().Json(http.StatusOK, http.Json{
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
// @Failure      400      {object}  ErrorResponse
// @Failure      401      {object}  ErrorResponse
// @Failure      500      {object}  ErrorResponse
// @Router       /users/me/totp/verify [post]
func ConfirmTOTP(ctx http.Context) http.Response {
	user := ctx.Value("user").(*models.User)

	var req requests.ConfirmTwoFactorRequest
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}

	if user.TotpSecret == "" {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "no TOTP secret found — call setup first"})
	}

	decryptedSecret, err := facades.Crypt().DecryptString(user.TotpSecret)
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to decrypt secret"})
	}

	if !userAuthService.VerifyTOTP(decryptedSecret, req.Code) {
		return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "invalid verification code"})
	}

	if err := container.Get().UserRepo.EnableTotp(user.ID); err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to enable 2FA"})
	}

	codes, hashes, err := userAuthService.GenerateRecoveryCodes()
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to generate recovery codes"})
	}

	_ = container.Get().TotpRecoveryCodeRepo.DeleteByUserID(user.ID)

	var recoveryCodes []models.TotpRecoveryCode
	for _, h := range hashes {
		recoveryCodes = append(recoveryCodes, models.TotpRecoveryCode{
			ID:       uuid.New(),
			UserID:   user.ID,
			CodeHash: h,
		})
	}
	_ = container.Get().TotpRecoveryCodeRepo.CreateBatch(recoveryCodes)

	user.TotpEnabled = true
	resp := map[string]interface{}{
		"user":           user,
		"recovery_codes": codes,
	}
	return ctx.Response().Json(http.StatusOK, resp)
}

// DisableTOTP godoc
// @Summary      Disable TOTP
// @Description  Disables 2FA and clears TOTP secret and recovery codes
// @Tags         User
// @Security     BearerAuth
// @Produce      json
// @Success      200  {object}  models.User
// @Failure      500  {object}  ErrorResponse
// @Router       /users/me/totp [delete]
func DisableTOTP(ctx http.Context) http.Response {
	user := ctx.Value("user").(*models.User)

	if err := container.Get().UserRepo.DisableTotp(user.ID); err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to disable 2FA"})
	}

	_ = container.Get().TotpRecoveryCodeRepo.DeleteByUserID(user.ID)

	user.TotpEnabled = false
	user.TotpSecret = ""
	return ctx.Response().Json(http.StatusOK, user)
}

// ---- Swagger-only types ----

type UpdateMeSwagger struct {
	FullName string `json:"full_name" example:"Alice Smith"`
}

type ChangePasswordSwagger struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

type UpdateDefaultAccountSwagger struct {
	AccountID string `json:"account_id" example:"550e8400-e29b-41d4-a716-446655440000"`
}

type AccountListResponse struct {
	Data []models.Account `json:"data"`
}

type TotpSetupSwagger struct {
	Secret string `json:"secret" example:"JBSWY3DPEHPK3PXP"`
	QrURL  string `json:"qr_url" example:"otpauth://totp/..."`
}

type ConfirmTotpSwagger struct {
	Code string `json:"code" example:"123456"`
}
