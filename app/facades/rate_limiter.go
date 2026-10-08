package facades

import (
	"github.com/goravel/framework/contracts/http"
	goravelfacades "github.com/goravel/framework/facades"
)

// RateLimiter returns the process rate limiter registry. Limiters are looked up by name.
func RateLimiter() http.RateLimiter {
	return goravelfacades.RateLimiter()
}
