package middleware

import (
	"strings"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/models"
)

// OptionalSessionUser is the user of the session bearer token a guest route
// may carry (accepting an invite while signed in). No bearer is nil, nil; a
// bearer that does not parse, or names no user, is the error. It checks nothing
// else: the route's service decides what that user may do.
func OptionalSessionUser(ctx http.Context) (*models.User, error) {
	bearer := ctx.Request().Header("Authorization", "")
	if !strings.HasPrefix(bearer, "Bearer ") {
		return nil, nil
	}
	guard := facades.Auth(ctx)
	if _, err := guard.Parse(strings.TrimPrefix(bearer, "Bearer ")); err != nil {
		return nil, err
	}
	var user models.User
	if err := guard.User(&user); err != nil {
		return nil, err
	}
	return &user, nil
}
