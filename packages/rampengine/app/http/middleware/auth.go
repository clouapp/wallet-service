package middleware

import (
	"context"
	"errors"
	"fmt"
	"strings"

	contractsauth "github.com/goravel/framework/contracts/auth"
	contractshttp "github.com/goravel/framework/contracts/http"
	frameworkerrors "github.com/goravel/framework/errors"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/http/responses"
	"github.com/macrowallets/waas/packages/rampengine/app/identity"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// userKey and tokenKey are unexported so that Auth is the only code that can
// put a client user, or the token it accepted, in a request context: whatever
// CurrentUser returns has passed the token, guard and revocation checks.
type (
	userKey  struct{}
	tokenKey struct{}
)

// Auth authenticates the client user and hands it to the handler through
// CurrentUser. It answers 401 "unauthenticated" when the token no longer names
// a live user; see AuthAnswering.
func Auth() contractshttp.Middleware {
	return AuthAnswering("unauthenticated")
}

// AuthAnswering is Auth whose 401 for a token that no longer names a live user
// says gone: the MFA routes promised the app "unauthorized" there.
//
// The user is loaded, not read off the token, on purpose: a token outlives the
// account it was issued for and the sessions a password reset revoked. A
// lookup that failed answers 500, because the app reads 401 as a finished
// session and a database outage answered 401 would sign every phone out.
func AuthAnswering(gone string) contractshttp.Middleware {
	return func(ctx contractshttp.Context) {
		header := ctx.Request().Header("Authorization", "")
		token := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
		if token == "" {
			responses.Error(ctx, contractshttp.StatusUnauthorized, "missing authorization token").Abort()
			return
		}
		payload, err := facades.Auth(ctx).Parse(token)
		if err != nil || payload.Guard != identity.GuardUser {
			responses.Error(ctx, contractshttp.StatusUnauthorized, "invalid or expired token").Abort()
			return
		}
		// Recorded before the user is loaded: the request log attributes a
		// request to the token the guard accepted, even when its account is gone.
		ctx.WithContext(context.WithValue(ctx.Context(), tokenKey{}, payload))

		var user models.User
		err = facades.Auth(ctx).User(&user)
		switch {
		case err == nil && user.ID != 0 && user.AcceptsTokenIssuedAt(payload.IssuedAt):
			ctx.WithContext(context.WithValue(ctx.Context(), userKey{}, &user))
			ctx.Request().Next()
		// The guard's provider uses FindOrFail, so a deleted (or soft-deleted)
		// account is OrmRecordNotFound.
		case err == nil, errors.Is(err, frameworkerrors.OrmRecordNotFound):
			responses.Error(ctx, contractshttp.StatusUnauthorized, gone).Abort()
		default:
			responses.InternalError(ctx, fmt.Errorf("loading the authenticated user: %w", err)).Abort()
		}
	}
}

// CurrentUser is the client user Auth authenticated. Every route behind Auth
// has one; it is nil only on a route Auth does not guard.
func CurrentUser(ctx contractshttp.Context) *models.User {
	user, _ := ctx.Context().Value(userKey{}).(*models.User)
	return user
}

// acceptedToken is the client token Auth accepted for this request, if any.
// Parse stores a token's claims before the guard is checked, so the guard
// itself also answers for a token Auth went on to refuse: an admin's, whose
// secret the client guard shares.
func acceptedToken(ctx contractshttp.Context) (*contractsauth.Payload, bool) {
	token, ok := ctx.Context().Value(tokenKey{}).(*contractsauth.Payload)
	return token, ok && token != nil && token.Key != ""
}
