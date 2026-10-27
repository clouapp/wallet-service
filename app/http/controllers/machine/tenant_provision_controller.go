package machine

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"os"
	"strings"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/tenantprovision"
)

type TenantProvisionController struct{}

type provisionRequest struct {
	ExternalID  string `form:"external_id" json:"external_id"`
	Name        string `form:"name" json:"name"`
	Environment string `form:"environment" json:"environment"`
}

// ValidProvisionKey uses an independent secret, never WALLET_SERVICE_KEY,
// whose purpose is encryption. A missing or short key disables this route.
func ValidProvisionKey(expected, presented string) bool {
	if len(expected) < 32 || len(presented) < 32 {
		return false
	}
	want := sha256.Sum256([]byte(expected))
	got := sha256.Sum256([]byte(presented))
	return subtle.ConstantTimeCompare(want[:], got[:]) == 1
}

func (TenantProvisionController) Store(ctx http.Context) http.Response {
	key := os.Getenv("TENANT_PROVISIONING_KEY")
	if len(key) < 32 {
		return responses.Fail(ctx, http.StatusServiceUnavailable, responses.CodeInvalidRequest, "tenant provisioning unavailable")
	}
	if !ValidProvisionKey(key, ctx.Request().Header("X-Tenant-Provisioning-Key", "")) {
		return responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "invalid provisioning key")
	}

	var req provisionRequest
	if err := ctx.Request().Bind(&req); err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid request body")
	}
	if strings.TrimSpace(req.ExternalID) == "" || strings.TrimSpace(req.Name) == "" ||
		(req.Environment != models.EnvironmentTest && req.Environment != models.EnvironmentProd) {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid tenant account")
	}

	svc := tenantprovision.Service{
		Accounts:     repositories.NewAccountRepository(nil),
		Tokens:       repositories.NewAccessTokenRepository(nil),
		SigningKey:   facades.Config().GetString("jwt.secret"),
		ProvisionKey: key,
	}
	result, err := svc.Provision(ctx.Context(), req.ExternalID, req.Name, req.Environment)
	if err != nil {
		if errors.Is(err, tenantprovision.ErrConflict) {
			return responses.Fail(ctx, http.StatusConflict, responses.CodeInvalidRequest, "tenant account conflict")
		}
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInvalidRequest, "tenant provisioning failed")
	}

	status := http.StatusOK
	if result.Created {
		status = http.StatusCreated
	}
	return ctx.Response().Status(status).Json(map[string]any{
		"account_id":  result.AccountID.String(),
		"api_token":   result.APIToken,
		"environment": req.Environment,
	})
}
