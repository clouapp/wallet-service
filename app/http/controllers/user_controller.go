package controllers

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
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
		if err := container.Get().UserRepo.UpdateFullName(ctx.Context(), user.ID, req.FullName); err != nil {
			return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to update profile"})
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
		return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "current password is incorrect"})
	}

	hash, err := userAuthService.HashPassword(req.NewPassword)
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to hash password"})
	}

	if err := container.Get().UserRepo.UpdatePasswordHash(ctx.Context(), user.ID, hash); err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to update password"})
	}

	return ctx.Response().Json(http.StatusOK, http.Json{"message": "password updated successfully"})
}

const (
	myAccountsDefaultLimit    = 20
	myAccountsMaxLimit        = 100
	myAccountsSearchMaxLength = 100
)

var myAccountsBounds = pagination.Bounds{DefaultLimit: myAccountsDefaultLimit, MaxLimit: myAccountsMaxLimit}

// ListMyAccounts godoc
// @Summary      List accounts for current user
// @Description  Returns a paginated list of accounts the authenticated user is a member of, ordered by name. A limit above 100 is capped; an offset past the end returns an empty page with the real total.
// @Tags         User
// @Security     BearerAuth
// @Produce      json
// @Param        limit        query   int     false  "Page size, 1-100 (default 20)"                 example(20)
// @Param        offset       query   int     false  "Rows to skip, >= 0 (default 0)"                example(0)
// @Param        search       query   string  false  "Case-insensitive match on name or id (max 100 chars)"
// @Param        environment  query   string  false  "Only accounts in this environment"             Enums(prod, test)
// @Success      200  {object}  AccountListResponse
// @Failure      400  {object}  ErrorResponse
// @Failure      401  {object}  ErrorResponse
// @Router       /users/me/accounts [get]
func ListMyAccounts(ctx http.Context) http.Response {
	userID := ctx.Value("user_id").(uuid.UUID)

	limit, offset, err := pagination.ParseStrict(ctx.Request().Query("limit", ""), ctx.Request().Query("offset", ""), myAccountsBounds)
	if err != nil {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": err.Error()})
	}

	filter, errMessage := parseMyAccountsFilter(ctx)
	if errMessage != "" {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": errMessage})
	}

	accounts, total, err := container.Get().AccountRepo.PaginateByMember(ctx.Context(), userID, filter, limit, offset)
	if err != nil {
		facades.Log().WithContext(ctx).Errorf("user: list my accounts: %v", err)
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to fetch accounts"})
	}

	return ctx.Response().Json(http.StatusOK, pagination.Response(accounts, total, limit, offset))
}

func parseMyAccountsFilter(ctx http.Context) (repositories.AccountListFilter, string) {
	search := strings.TrimSpace(ctx.Request().Query("search", ""))
	if utf8.RuneCountInString(search) > myAccountsSearchMaxLength {
		return repositories.AccountListFilter{}, fmt.Sprintf("search must be at most %d characters", myAccountsSearchMaxLength)
	}

	environment := strings.TrimSpace(ctx.Request().Query("environment", ""))
	if environment != "" && environment != models.EnvironmentProd && environment != models.EnvironmentTest {
		return repositories.AccountListFilter{}, fmt.Sprintf("environment must be %q or %q", models.EnvironmentProd, models.EnvironmentTest)
	}

	return repositories.AccountListFilter{Search: search, Environment: environment}, ""
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

	au, err := container.Get().AccountUserRepo.FindByAccountAndUser(ctx.Context(), accountID, userID)
	if err != nil || au == nil {
		return responses.Send(ctx, http.StatusForbidden, http.Json{"error": "not a member of this account"})
	}

	userPtr, _ := container.Get().UserRepo.FindByID(ctx.Context(), userID)
	if userPtr == nil {
		return responses.Send(ctx, http.StatusNotFound, http.Json{"error": "user not found"})
	}
	userPtr.DefaultAccountID = &accountID
	if err := container.Get().UserRepo.UpdateDefaultAccountID(ctx.Context(), userPtr.ID, &accountID); err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to update default account"})
	}

	account, _ := container.Get().AccountRepo.FindByID(ctx.Context(), accountID)
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
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to generate TOTP secret"})
	}

	encryptedSecret, err := facades.Crypt().EncryptString(secret)
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to encrypt secret"})
	}

	if err := container.Get().UserRepo.UpdateTotpSecret(ctx.Context(), user.ID, encryptedSecret); err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to save TOTP secret"})
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
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "no TOTP secret found — call setup first"})
	}

	decryptedSecret, err := facades.Crypt().DecryptString(user.TotpSecret)
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to decrypt secret"})
	}

	if !userAuthService.VerifyTOTP(decryptedSecret, req.Code) {
		return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "invalid verification code"})
	}

	if err := container.Get().UserRepo.EnableTotp(ctx.Context(), user.ID); err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to enable 2FA"})
	}

	codes, hashes, err := userAuthService.GenerateRecoveryCodes()
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to generate recovery codes"})
	}

	_ = container.Get().TotpRecoveryCodeRepo.DeleteByUserID(ctx.Context(), user.ID)

	var recoveryCodes []models.TotpRecoveryCode
	for _, h := range hashes {
		recoveryCodes = append(recoveryCodes, models.TotpRecoveryCode{
			ID:       uuid.New(),
			UserID:   user.ID,
			CodeHash: h,
		})
	}
	_ = container.Get().TotpRecoveryCodeRepo.CreateBatch(ctx.Context(), recoveryCodes)

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

	if err := container.Get().UserRepo.DisableTotp(ctx.Context(), user.ID); err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to disable 2FA"})
	}

	_ = container.Get().TotpRecoveryCodeRepo.DeleteByUserID(ctx.Context(), user.ID)

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
	Data   []models.Account `json:"data"`
	Total  int64            `json:"total" example:"64"`
	Limit  int              `json:"limit" example:"20"`
	Offset int              `json:"offset" example:"0"`
}

type TotpSetupSwagger struct {
	Secret string `json:"secret" example:"JBSWY3DPEHPK3PXP"`
	QrURL  string `json:"qr_url" example:"otpauth://totp/..."`
}

type ConfirmTotpSwagger struct {
	Code string `json:"code" example:"123456"`
}
