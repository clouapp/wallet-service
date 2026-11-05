package contracts

import "context"

// Feature is the developer-facing feature-flag API, the one the Feature facade
// resolves. The first eight methods mirror Laravel Pennant's Driver contract;
// InvalidateCache is an operational lever with no Pennant analogue; the last
// five are ergonomic helpers. Define's options have no Pennant analogue either:
// GatedByGlobal is what IsActive, IsInactive and Active answer through.
type Feature interface {
	Define(feature string, resolver Resolver, options ...DefineOption)
	Defined() []string
	Get(ctx context.Context, feature string, scope any) (any, error)
	GetAll(ctx context.Context, features map[string][]any) (map[string]map[string]any, error)
	Set(ctx context.Context, feature string, scope, value any) error
	SetForAllScopes(ctx context.Context, feature string, value any) error
	Delete(ctx context.Context, feature string, scope any) error
	Purge(ctx context.Context, features []string) error
	// InvalidateCache drops the driver's cache leg, if it has one, and reports
	// whether there was one. It never touches the authoritative values.
	InvalidateCache(ctx context.Context) (cleared bool, err error)

	IsActive(ctx context.Context, feature string, scope any) (bool, error)
	IsInactive(ctx context.Context, feature string, scope any) (bool, error)
	Activate(ctx context.Context, feature string, scope any) error
	Deactivate(ctx context.Context, feature string, scope any) error
	// Active returns which of features are on for scope, in the order given
	// and never nil. A feature that cannot be read is left out and reported in
	// the error, which is returned alongside the rest of the answer.
	Active(ctx context.Context, features []string, scope any) ([]string, error)
}

// DefineOptions is how Define is configured beyond the resolver.
type DefineOptions struct {
	// GatedByGlobal makes the global value a veto over every other scope:
	// IsActive and IsInactive answer for a scope only while the global value is
	// on too. Get still returns the scope's own value.
	GatedByGlobal bool
}

// DefineOption sets one of the DefineOptions.
type DefineOption func(*DefineOptions)

// Resolver computes a feature's value for a scope the first time it is read. It
// receives the ORIGINAL scope (e.g. *User), not the resolved key, and a nil
// scope for the global scope.
type Resolver func(ctx context.Context, scope any) (any, error)
