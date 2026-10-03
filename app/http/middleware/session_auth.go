package middleware

import (
	"strings"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
)

// SessionAuth validates a Bearer JWT token issued by facades.Auth and injects
// "user_id" and "user" into the request context for downstream handlers.
func SessionAuth() http.Middleware {
	return func(ctx http.Context) {
		bearer := ctx.Request().Header("Authorization", "")
		if !strings.HasPrefix(bearer, "Bearer ") {
			_ = responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "missing or malformed bearer token"}).Abort()
			return
		}
		token := strings.TrimPrefix(bearer, "Bearer ")

		authGuard := facades.Auth(ctx)
		payload, err := authGuard.Parse(token)
		if err != nil || payload == nil {
			_ = responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "invalid token"}).Abort()
			return
		}

		var user models.User
		if err := authGuard.User(&user); err != nil {
			_ = responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "user not found"}).Abort()
			return
		}

		ctx.WithValue(requestctx.KeyUserID, user.ID)
		ctx.WithValue(requestctx.KeyUser, &user)
		ctx.Request().Next()
	}
}

// contextUserID extracts the user UUID from the request context.
func contextUserID(ctx http.Context) uuid.UUID {
	id, _ := requestctx.UserID(ctx)
	return id
}

// SessionUserID is the dashboard user SessionAuth stored. A missing value is
// uuid.Nil. Callers outside middleware use this instead of reading the key.
func SessionUserID(ctx http.Context) uuid.UUID {
	if ctx == nil {
		return uuid.Nil
	}
	return contextUserID(ctx)
}
