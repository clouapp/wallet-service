// Package catalog lists every application feature flag, registers them on the
// manager — Register is called from FeatureServiceProvider.Boot — and answers
// the admin UI's questions about them.
package catalog

import (
	"context"
	"slices"

	"github.com/macrowallets/waas/packages/rampengine/app/featureflags"
	adminflags "github.com/macrowallets/waas/packages/rampengine/app/featureflags/admin"
	customersflags "github.com/macrowallets/waas/packages/rampengine/app/featureflags/customers"
	identityflags "github.com/macrowallets/waas/packages/rampengine/app/featureflags/identity"
	kycflags "github.com/macrowallets/waas/packages/rampengine/app/featureflags/kyc"
	rampflags "github.com/macrowallets/waas/packages/rampengine/app/featureflags/ramp"
	requestlogflags "github.com/macrowallets/waas/packages/rampengine/app/featureflags/requestlog"
	"github.com/macrowallets/waas/packages/rampengine/packages/featureflag"
	"github.com/macrowallets/waas/packages/rampengine/packages/featureflag/contracts"
)

// all is every flag, one domain after another. Keep it sorted by Name — the
// admin UI renders it in that order.
var all = slices.Concat(
	adminflags.Definitions(),
	identityflags.Definitions(),
	customersflags.Definitions(),
	kycflags.Definitions(),
	rampflags.Definitions(),
	requestlogflags.Definitions(),
)

// ForScope returns the flags that may be toggled for the given scope type.
func ForScope(s featureflags.ScopeType) []featureflags.Definition {
	out := make([]featureflags.Definition, 0, len(all))
	for _, d := range all {
		if d.AppliesTo(s) {
			out = append(out, d)
		}
	}
	return out
}

// NamesFor returns the names of the flags that may be toggled for the given
// scope type, which is what the manager's Active takes.
func NamesFor(s featureflags.ScopeType) []string {
	defs := ForScope(s)
	out := make([]string, 0, len(defs))
	for _, d := range defs {
		out = append(out, d.Name)
	}
	return out
}

// Find returns the definition of a flag by name.
func Find(name string) (featureflags.Definition, bool) {
	for _, d := range all {
		if d.Name == name {
			return d, true
		}
	}
	return featureflags.Definition{}, false
}

// Register declares every catalogued feature flag on the manager. Defaults are
// constant per flag: a value that differs per environment belongs in config +
// .env, not here; an environment that wants a flag on sets it in the admin.
//
// A flag declaring ScopeGlobal is defined gated by it: the product-wide value
// the admin panel toggles is then a veto every other scope answers to, however
// the flag is read.
func Register(m contracts.Feature) {
	for _, d := range all {
		value := d.Default
		var options []contracts.DefineOption
		if d.AppliesTo(featureflags.ScopeGlobal) {
			options = append(options, featureflag.GatedByGlobal())
		}
		m.Define(d.Name, func(_ context.Context, _ any) (any, error) {
			return value, nil
		}, options...)
	}
}
