package middleware

import (
	"strings"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

// SessionAuth validates a Bearer JWT token issued by facades.Auth and injects
// "user_id" and "user" into the request context for downstream handlers.
// Returns 401 if the token is absent, invalid, or the user no longer exists.
func SessionAuth(ctx http.Context) {
	bearer := ctx.Request().Header("Authorization", "")
	if !strings.HasPrefix(bearer, "Bearer ") {
		ctx.Request().AbortWithStatus(http.StatusUnauthorized)
		ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "missing or malformed bearer token"})
		return
	}
	token := strings.TrimPrefix(bearer, "Bearer ")

	authGuard := facades.Auth(ctx)
	payload, err := authGuard.Parse(token)
	if err != nil || payload == nil {
		ctx.Request().AbortWithStatus(http.StatusUnauthorized)
		ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "invalid token"})
		return
	}

	var user models.User
	if err := authGuard.User(&user); err != nil {
		ctx.Request().AbortWithStatus(http.StatusUnauthorized)
		ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "user not found"})
		return
	}

	ctx.WithValue("user_id", user.ID)
	ctx.WithValue("user", &user)
	ctx.Request().Next()
}

// contextUserID extracts the user UUID from the request context.
// Returns uuid.Nil if not set.
func contextUserID(ctx http.Context) uuid.UUID {
	id, _ := ctx.Value("user_id").(uuid.UUID)
	return id
}
