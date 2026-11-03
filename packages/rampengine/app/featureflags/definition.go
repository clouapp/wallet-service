package featureflags

import "slices"

// Definition describes one feature flag: its storage name, how the admin UI
// presents it, which scope types it may be toggled for, and the value its
// resolver returns when no override is stored.
type Definition struct {
	Name        string
	Label       string
	Description string
	Scopes      []ScopeType
	Default     bool
}

// AppliesTo reports whether this flag may be toggled for the given scope type.
func (d Definition) AppliesTo(s ScopeType) bool {
	return slices.Contains(d.Scopes, s)
}

// ScopeNames returns the scope types this flag accepts, as the strings the API
// speaks. Always non-nil, so a flag accepting none serializes as [] rather than
// null.
func (d Definition) ScopeNames() []string {
	out := make([]string, 0, len(d.Scopes))
	for _, s := range d.Scopes {
		out = append(out, string(s))
	}
	return out
}
