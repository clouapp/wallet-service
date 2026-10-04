package facades

import (
	"github.com/goravel/framework/contracts/auth"
	"github.com/goravel/framework/contracts/http"
	goravelfacades "github.com/goravel/framework/facades"
)

// Auth returns the process guard. Callers keep the same login, parse, and logout calls.
func Auth(ctx ...http.Context) auth.Auth {
	return goravelfacades.Auth(ctx...)
}
