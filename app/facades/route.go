package facades

import (
	"github.com/goravel/framework/contracts/route"
	goravelfacades "github.com/goravel/framework/facades"
)

// Route returns the process HTTP router. Callers keep the same routes, middleware, and handlers.
func Route() route.Route {
	return goravelfacades.Route()
}
