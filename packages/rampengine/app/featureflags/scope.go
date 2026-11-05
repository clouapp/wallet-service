package featureflags

// ScopeType is a kind of entity a flag can be scoped to. The string value is
// what the admin API accepts in the {scope} path segment.
type ScopeType string

const (
	ScopeAdmin ScopeType = "admin"
	ScopeUser  ScopeType = "user"
	// ScopeGlobal is the product itself — the reserved unscoped row in the flag
	// store, not an entity. It has no {id}.
	ScopeGlobal ScopeType = "global"
)

// ParseScopeType maps an API path segment to a ScopeType. The match is exact:
// unknown or differently-cased input is rejected rather than coerced.
func ParseScopeType(s string) (ScopeType, bool) {
	switch ScopeType(s) {
	case ScopeAdmin:
		return ScopeAdmin, true
	case ScopeUser:
		return ScopeUser, true
	case ScopeGlobal:
		return ScopeGlobal, true
	}
	return "", false
}

// TakesTarget reports whether this scope type names an entity to load.
//
// It exists because ParseScopeType has to accept "global" for the global
// endpoint to work, and that same acceptance makes "global" a syntactically
// valid {scope} segment on the route that carries an {id}. Without this check,
// GET /admin/api/features/global/5 would parse, ignore the 5, and serve the
// product-wide listing as though it were user 5's.
func (s ScopeType) TakesTarget() bool {
	return s == ScopeAdmin || s == ScopeUser
}
