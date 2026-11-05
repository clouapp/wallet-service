package featureflag

import "github.com/goravel/framework/errors"

// The package's refusals. Each is matched with errors.Is, which compares the
// unformatted text, so a sentinel still matches after Args fills it in.
var (
	ErrServiceProviderNotRegistered = errors.New("please register the featureflag service provider").SetModule(Name)

	// ErrFeatureNotDefined is returned by a manager Get when a feature has
	// neither a stored value nor a registered resolver.
	ErrFeatureNotDefined = errors.New("feature %q is not defined").SetModule(Name)
	// ErrUnresolvableScope is returned when a scope cannot be mapped to a
	// storage key (unsupported type, or the reserved global key).
	ErrUnresolvableScope = errors.New("cannot resolve scope to identifier: %s").SetModule(Name)

	// The configuration errors DriverCreator answers at boot, when the
	// container first resolves the manager.
	ErrStoreNotConfigured    = errors.New("store %q is not configured").SetModule(Name)
	ErrDriverNotSupported    = errors.New("unknown driver %q for store %q").SetModule(Name)
	ErrCachedStoreIncomplete = errors.New(`store %q needs both a "cache" and a "store"`).SetModule(Name)
	ErrStoreCycle            = errors.New("store %q is defined in terms of itself (%s)").SetModule(Name)
)
