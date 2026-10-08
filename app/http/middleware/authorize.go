package middleware

import (
	contractsaccess "github.com/goravel/framework/contracts/auth/access"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/policies"
)

// outcome is what a guard's subject settled before the Gate is asked.
type outcome int

const (
	// decide asks the Gate with the subject's arguments.
	decide outcome = iota
	// pass leaves the request to the handler, which answers 400 or 404 for a
	// child that is malformed or missing, so 404 stays ahead of 403.
	pass
	// answered means the subject already wrote the response (a failed read is 503).
	answered
)

// subject is what one guard hands the Gate: the ability's arguments, read
// from what the scope middleware already loaded, or an outcome that ends the
// guard first.
type subject func(ctx http.Context) (map[string]any, outcome)

// authorize is the permission guard behind Can, AccountUpdateMember, the
// May* guards and the Wallet* guards. It asks the Gate for ability with the
// subject's arguments and answers a refusal with 403 and the ability's
// sentence, written by abortWithJSON as those guards always wrote it. The
// Gate answers an ability it does not know with "ability doesn't exist", so
// a guard is never built for one.
func authorize(gate contractsaccess.Gate, ability string, subject subject) http.Middleware {
	if gate == nil {
		panic("authorize: the gate is required")
	}
	if !policies.IsAbility(ability) {
		panic("authorize: " + ability + " is not a gate ability")
	}
	return func(ctx http.Context) {
		arguments, settled := subject(ctx)
		switch settled {
		case pass:
			ctx.Request().Next()
			return
		case answered:
			return
		}
		if response := gate.WithContext(ctx).Inspect(ability, arguments); !response.Allowed() {
			abortWithJSON(ctx, http.StatusForbidden, http.Json{"error": response.Message()})
			return
		}
		ctx.Request().Next()
	}
}
