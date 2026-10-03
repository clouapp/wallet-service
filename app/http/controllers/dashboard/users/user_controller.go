package users

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/http/controllers"
	dashboardaccounts "github.com/macrowallets/waas/app/http/controllers/dashboard/accounts"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	accountsvc "github.com/macrowallets/waas/app/services/account"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/app/services/sessions"
	usersvc "github.com/macrowallets/waas/app/services/users"
)

func validateRequest(ctx http.Context, req http.FormRequest) http.Response {
	return controllers.ValidateRequest(ctx, req)
}

type UsersController struct {
	users        *usersvc.Service
	accounts     *accountsvc.Service
	passwords    *authsvc.Service
	refresh      *sessions.RefreshTokens
	secondFactor *authsvc.SecondFactorVerifier
	revoker      *authsvc.SessionRevoker
}

// NewUsersController wires the dashboard user handlers. Services are the
// provider singletons, resolved once at boot.
func NewUsersController(
	users *usersvc.Service,
	accounts *accountsvc.Service,
	passwords *authsvc.Service,
	refresh *sessions.RefreshTokens,
	secondFactor *authsvc.SecondFactorVerifier,
	revoker *authsvc.SessionRevoker,
) *UsersController {
	if users == nil {
		panic("dashboard users controller: users service is required")
	}
	if accounts == nil {
		panic("dashboard users controller: account service is required")
	}
	if passwords == nil {
		panic("dashboard users controller: auth service is required")
	}
	if refresh == nil {
		panic("dashboard users controller: refresh tokens are required")
	}
	if secondFactor == nil {
		panic("dashboard users controller: second factor verifier is required")
	}
	if revoker == nil {
		panic("dashboard users controller: session revoker is required")
	}
	return &UsersController{
		users:        users,
		accounts:     accounts,
		passwords:    passwords,
		refresh:      refresh,
		secondFactor: secondFactor,
		revoker:      revoker,
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
// @Success      200  {object}  models.User
// @Failure      401  {object}  ErrorResponse
// @Router       /users/me [get]
func (ctrl *UsersController) GetMe(ctx http.Context) http.Response {
	user := requestctx.MustUser(ctx)
	return responses.Send(ctx, http.StatusOK, user)
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
func (ctrl *UsersController) UpdateMe(ctx http.Context) http.Response {
	user := requestctx.MustUser(ctx)

	var req requests.UpdateMeRequest
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}

	if req.FullName != "" {
		if err := ctrl.users.UpdateFullName(ctx.Context(), user.ID, req.FullName); err != nil {
			return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to update profile"})
		}
		user.FullName = req.FullName
	}

	return responses.Send(ctx, http.StatusOK, user)
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
func (ctrl *UsersController) ChangePassword(ctx http.Context) http.Response {
	user := requestctx.MustUser(ctx)

	var req requests.ChangePasswordRequest
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}

	if !ctrl.passwords.CheckPassword(req.CurrentPassword, user.PasswordHash) {
		return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "current password is incorrect"})
	}

	hash, err := ctrl.passwords.HashPassword(req.NewPassword)
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to hash password"})
	}

	if err := ctrl.users.UpdatePasswordHash(ctx.Context(), user.ID, hash); err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to update password"})
	}

	session, err := ctrl.sessions().ReplaceSessions(ctx, user.ID)
	if err != nil {
		appfacades.Log().WithContext(ctx).Errorf("auth: change password: replace sessions: %v", err)
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "password updated but sessions could not be renewed"})
	}
	return responses.Send(ctx, http.StatusOK, http.Json{
		"message":       "password updated successfully",
		"access_token":  session.AccessToken,
		"refresh_token": session.RefreshToken,
	})
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
func (ctrl *UsersController) ListMyAccounts(ctx http.Context) http.Response {
	userID := requestctx.MustUserID(ctx)

	var query requests.ListMyAccountsRequest
	query.Load(ctx)
	limit, offset, err := pagination.ParseStrict(query.Limit, query.Offset, myAccountsBounds)
	if err != nil {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": err.Error()})
	}

	search, environment, errMessage := parseMyAccountsFilter(query.Search, query.Environment)
	if errMessage != "" {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": errMessage})
	}

	accounts, total, err := ctrl.accounts.ListForMember(ctx.Context(), userID, search, environment, limit, offset)
	if err != nil {
		appfacades.Log().WithContext(ctx).Errorf("user: list my accounts: %v", err)
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to fetch accounts"})
	}

	items, err := ctrl.accountsWithCallerRole(ctx, userID, accounts)
	if err != nil {
		appfacades.Log().WithContext(ctx).Errorf("user: list my accounts roles: %v", err)
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to fetch accounts"})
	}

	return responses.Send(ctx, http.StatusOK, pagination.Response(items, total, limit, offset))
}

// accountsWithCallerRole copies each account and adds the caller's stored
// membership role. A listed account with no active role is a broken
// membership and fails the request instead of omitting the field.
func (ctrl *UsersController) accountsWithCallerRole(ctx http.Context, userID uuid.UUID, accounts []models.Account) ([]myAccount, error) {
	items := make([]myAccount, 0, len(accounts))
	if len(accounts) == 0 {
		return items, nil
	}

	accountIDs := make([]uuid.UUID, len(accounts))
	for i, account := range accounts {
		accountIDs[i] = account.ID
	}
	roles, err := ctrl.accounts.RolesForUserAccounts(ctx.Context(), userID, accountIDs)
	if err != nil {
		return nil, err
	}
	for _, account := range accounts {
		role, ok := roles[account.ID]
		if !ok || strings.TrimSpace(role) == "" {
			return nil, fmt.Errorf("account %s has no role for user %s", account.ID, userID)
		}
		items = append(items, myAccount{AccountView: dashboardaccounts.NewAccountView(account), Role: role})
	}
	return items, nil
}

func parseMyAccountsFilter(search, environment string) (string, string, string) {
	search = strings.TrimSpace(search)
	if utf8.RuneCountInString(search) > myAccountsSearchMaxLength {
		return "", "", fmt.Sprintf("search must be at most %d characters", myAccountsSearchMaxLength)
	}

	environment = strings.TrimSpace(environment)
	if environment != "" && environment != models.EnvironmentProd && environment != models.EnvironmentTest {
		return "", "", fmt.Sprintf("environment must be %q or %q", models.EnvironmentProd, models.EnvironmentTest)
	}

	return search, environment, ""
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
func (ctrl *UsersController) UpdateDefaultAccount(ctx http.Context) http.Response {
	userID := requestctx.MustUserID(ctx)

	var req requests.UpdateDefaultAccountRequest
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}

	accountID, _ := uuid.Parse(req.AccountID)

	au, err := ctrl.accounts.FindMember(ctx.Context(), accountID, userID)
	if err != nil || au == nil {
		return responses.Send(ctx, http.StatusForbidden, http.Json{"error": "not a member of this account"})
	}

	userPtr, _ := ctrl.users.FindByID(ctx.Context(), userID)
	if userPtr == nil {
		return responses.Send(ctx, http.StatusNotFound, http.Json{"error": "user not found"})
	}
	userPtr.DefaultAccountID = &accountID
	if err := ctrl.users.UpdateDefaultAccountID(ctx.Context(), userPtr.ID, &accountID); err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to update default account"})
	}

	account, _ := ctrl.accounts.FindByID(ctx.Context(), accountID)
	return responses.Send(ctx, http.StatusOK, http.Json{"account": dashboardaccounts.AccountViewPtr(account)})
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
func (ctrl *UsersController) SetupTOTP(ctx http.Context) http.Response {
	user := requestctx.MustUser(ctx)
	if user.TotpEnabled {
		return responses.Send(ctx, http.StatusConflict, http.Json{"error": "2FA is already enabled"})
	}

	secret, qrURL, err := ctrl.passwords.GenerateTOTP(user.Email)
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to generate TOTP secret"})
	}

	encryptedSecret, err := appfacades.Crypt().EncryptString(secret)
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to encrypt secret"})
	}

	if err := ctrl.users.UpdateTotpSecret(ctx.Context(), user.ID, encryptedSecret); err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to save TOTP secret"})
	}

	return responses.Send(ctx, http.StatusOK, http.Json{
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
func (ctrl *UsersController) ConfirmTOTP(ctx http.Context) http.Response {
	user := requestctx.MustUser(ctx)

	var req requests.ConfirmTwoFactorRequest
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}

	if user.TotpSecret == "" {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "no TOTP secret found — call setup first"})
	}

	decryptedSecret, err := appfacades.Crypt().DecryptString(user.TotpSecret)
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to decrypt secret"})
	}

	matched, err := ctrl.secondFactor.RecordConfirmedCode(user.ID, decryptedSecret, req.Code)
	if err != nil {
		appfacades.Log().WithContext(ctx).Errorf("user: confirm totp: %v", err)
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to enable 2FA"})
	}
	if !matched {
		return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "invalid verification code"})
	}

	if err := ctrl.users.EnableTotp(ctx.Context(), user.ID); err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to enable 2FA"})
	}

	codes, hashes, err := ctrl.passwords.GenerateRecoveryCodes()
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to generate recovery codes"})
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
	resp := map[string]interface{}{
		"user":           user,
		"recovery_codes": codes,
	}
	return responses.Send(ctx, http.StatusOK, resp)
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
func (ctrl *UsersController) DisableTOTP(ctx http.Context) http.Response {
	sessionUser := requestctx.MustUser(ctx)
	user, err := ctrl.users.FindByID(ctx.Context(), sessionUser.ID)
	if err != nil || user == nil {
		return responses.Send(ctx, http.StatusNotFound, http.Json{"error": "user not found"})
	}
	if user.TotpEnabled {
		if resp := ctrl.requireLiveSecondFactor(ctx, user); resp != nil {
			return resp
		}
	}

	if err := ctrl.users.DisableTotp(ctx.Context(), user.ID); err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to disable 2FA"})
	}
	_ = ctrl.users.DeleteRecoveryCodes(ctx.Context(), user.ID)

	session, err := ctrl.sessions().ReplaceSessions(ctx, user.ID)
	if err != nil {
		appfacades.Log().WithContext(ctx).Errorf("auth: disable totp: replace sessions: %v", err)
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "2FA disabled but sessions could not be renewed"})
	}
	user.TotpEnabled = false
	user.TotpSecret = ""
	return responses.Send(ctx, http.StatusOK, http.Json{
		"user":          user,
		"access_token":  session.AccessToken,
		"refresh_token": session.RefreshToken,
	})
}

func (ctrl *UsersController) requireLiveSecondFactor(ctx http.Context, user *models.User) http.Response {
	var req requests.DisableTotpRequest
	if errResp := validateRequest(ctx, &req); errResp != nil {
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

type UpdateDefaultAccountSwagger struct {
	AccountID string `json:"account_id" example:"550e8400-e29b-41d4-a716-446655440000"`
}

// myAccount is one row of GET /v1/users/me/accounts. Existing account fields
// stay; role is the caller's account_users.role, returned as stored
// (owner, admin, auditor, or user).
type myAccount struct {
	dashboardaccounts.AccountView
	Role string `json:"role" example:"owner"`
}

type AccountListResponse struct {
	Data   []myAccount `json:"data"`
	Total  int64       `json:"total" example:"64"`
	Limit  int         `json:"limit" example:"20"`
	Offset int         `json:"offset" example:"0"`
}

type TotpSetupSwagger struct {
	Secret string `json:"secret" example:"JBSWY3DPEHPK3PXP"`
	QrURL  string `json:"qr_url" example:"otpauth://totp/..."`
}

type ConfirmTotpSwagger struct {
	Code string `json:"code" example:"123456"`
}
