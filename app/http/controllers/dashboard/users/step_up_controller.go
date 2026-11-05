package users

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/http/responses"
	accountsvc "github.com/macrowallets/waas/app/services/account"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	featuressvc "github.com/macrowallets/waas/app/services/features"
	"github.com/macrowallets/waas/app/services/stepup"
)

// StepUpController opens and reports the settings step-up window of the
// signed-in session.
type StepUpController struct {
	verifier *authsvc.SecondFactorVerifier
	windows  *stepup.Service
	features *featuressvc.Service
	accounts *accountsvc.Service
}

// NewStepUpController wires the step-up handlers: the verifier checks the
// code, the window service keeps the 15 minutes, the features service tells
// whether the flag is on and the account service checks the X-Account-Id.
func NewStepUpController(verifier *authsvc.SecondFactorVerifier, windows *stepup.Service, features *featuressvc.Service, accounts *accountsvc.Service) *StepUpController {
	if verifier == nil {
		panic("dashboard step-up controller: second factor verifier is required")
	}
	if windows == nil {
		panic("dashboard step-up controller: step-up service is required")
	}
	if features == nil {
		panic("dashboard step-up controller: feature flags are required")
	}
	if accounts == nil {
		panic("dashboard step-up controller: account service is required")
	}
	return &StepUpController{verifier: verifier, windows: windows, features: features, accounts: accounts}
}

// stepUpRequest is the body of POST /v1/users/me/step-up.
type stepUpRequest struct {
	TotpCode string `form:"totp_code" json:"totp_code"`
}

func (r *stepUpRequest) Authorize(ctx http.Context) error { return nil }

func (r *stepUpRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{"totp_code": "required"}
}

// Store godoc
//
//	@Summary		Open the settings step-up window
//	@Description	Checks the authenticator code and opens a fixed 15 minute window for this session
//	@Tags			User
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			request	body		StepUpRequestSwagger	true	"Authenticator code"
//	@Success		200		{object}	StepUpGrantedSwagger
//	@Failure		401		{object}	responses.ErrorBody
//	@Failure		422		{object}	responses.ErrorBody
//	@Failure		429		{object}	responses.ErrorBody	"Rate limit or too many attempts"
//	@Router			/users/me/step-up [post]
func (c *StepUpController) Store(ctx http.Context) http.Response {
	user := requestctx.MustUser(ctx)
	sid, ok := requestctx.SessionID(ctx)
	if !ok || sid == uuid.Nil {
		return responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "session has no id, sign in again")
	}

	var req stepUpRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}

	if err := c.verifier.Verify(user, req.TotpCode, ""); err != nil {
		if response := controllers.MapSecondFactorError(ctx, err); response != nil {
			return response
		}
		if errors.Is(err, authsvc.ErrSecondFactorNotEnrolled) {
			return responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "invalid 2FA code")
		}
		return internalError(ctx, err, "internal error")
	}

	expiresAt, err := c.windows.Grant(sid)
	if err != nil {
		return internalError(ctx, err, "failed to open step-up window")
	}
	return ctx.Response().Success().Json(map[string]any{"expires_at": expiresAt.Format(time.RFC3339)})
}

// Show godoc
//
//	@Summary		Read the settings step-up window
//	@Description	Says whether the step-up is required for this user (flag on and 2FA enabled) and when the session's window ends. X-Account-Id, when sent, adds the account's flag row.
//	@Tags			User
//	@Security		BearerAuth
//	@Produce		json
//	@Param			X-Account-Id	header		string	false	"Account whose flag row counts"
//	@Success		200				{object}	StepUpStatusSwagger
//	@Failure		400				{object}	responses.ErrorBody
//	@Failure		403				{object}	responses.ErrorBody
//	@Router			/users/me/step-up [get]
func (c *StepUpController) Show(ctx http.Context) http.Response {
	user := requestctx.MustUser(ctx)

	accountID := uuid.Nil
	if raw := ctx.Request().Header("X-Account-Id"); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid X-Account-Id")
		}
		member, err := c.accounts.FindMember(ctx.Context(), parsed, user.ID)
		if err != nil || member == nil {
			return responses.Fail(ctx, http.StatusForbidden, responses.CodeForbidden, "not a member of this account")
		}
		accountID = parsed
	}

	required := false
	if user.TotpEnabled {
		on, err := c.features.Settings2FARequired(ctx.Context(), accountID)
		if err != nil {
			return internalError(ctx, err, "internal error")
		}
		required = on
	}

	var expiresAt any
	if sid, ok := requestctx.SessionID(ctx); ok {
		window, err := c.windows.Status(sid)
		if err != nil {
			return internalError(ctx, err, "internal error")
		}
		if window != nil {
			expiresAt = window.Format(time.RFC3339)
		}
	}
	return ctx.Response().Success().Json(map[string]any{"required": required, "expires_at": expiresAt})
}
