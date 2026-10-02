package middleware

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/models"
)

// APIScope enforces the permission and IP restriction stored on the API token.
// It runs after APITokenAuth, which is what places the token on the context.
func APIScope(permission string) http.Middleware {
	return func(ctx http.Context) {
		token, _ := ctx.Value("api_token").(*models.AccessToken)
		if token == nil {
			abortWithJSON(ctx, http.StatusUnauthorized, http.Json{"error": "missing api token"})
			return
		}
		grants, err := models.ParseStoredAPIPermissions(token.Permissions)
		if err != nil || !models.APITokenAllows(grants, permission) {
			abortWithJSON(ctx, http.StatusForbidden, http.Json{"error": "insufficient api token scope"})
			return
		}
		if !models.ClientIPAllowed(token.IpCidr, ctx.Request().Ip()) {
			abortWithJSON(ctx, http.StatusForbidden, http.Json{"error": "api token ip not allowed"})
			return
		}
		ctx.Request().Next()
	}
}
