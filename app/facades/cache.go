package facades

import (
	contractscache "github.com/goravel/framework/contracts/cache"
	goravelfacades "github.com/goravel/framework/facades"
)

// Cache returns the process cache. Callers keep the same driver and keys.
func Cache() contractscache.Cache {
	return goravelfacades.Cache()
}
