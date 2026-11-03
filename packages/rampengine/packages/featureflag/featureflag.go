// Package featureflag provides Laravel Pennant-style feature flags as a
// Goravel package. Features are defined with a resolver callback; reads
// resolve on the first miss and persist (memoize) the result. Persistence is
// pluggable through the contracts.Driver interface; the drivers subpackage
// ships in-memory, Redis, database and cached (Redis in front of the database)
// drivers, and DriverCreator picks one by name from config/featureflag.go.
//
// ServiceProvider binds the manager under Binding, and facades.Feature
// resolves it. The manager and the drivers take every dependency through their
// constructors and reach for no facade, so they are as usable outside the
// container as inside it.
package featureflag

// globalScopeKey is the storage key for the nil (global) scope. It is reserved:
// ResolveScope rejects any scope that would resolve to this literal.
const globalScopeKey = "__global__"

// isActive reports whether a resolved value counts as "on": anything that is
// neither nil nor boolean false.
func isActive(value any) bool {
	if value == nil {
		return false
	}
	if b, ok := value.(bool); ok {
		return b
	}
	return true
}
