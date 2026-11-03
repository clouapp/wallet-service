package policies

import (
	"context"

	"github.com/goravel/framework/auth/access"
	contractsaccess "github.com/goravel/framework/contracts/auth/access"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// UpdateScopedFeatures is the ability to write one admin's or one user's
// feature flags (PUT /features/{scope}/{id}[/{feature}]).
const UpdateScopedFeatures = "update-scoped-features"

// ArgScope is the scope segment of the route: "user", "admin", or whatever
// else was typed there. ArgTargetID is the {id} beside it (a uint; 0 when it
// named nothing).
const (
	ArgScope    = "scope"
	ArgTargetID = "target_id"
)

// The scopes the policy tells apart. The feature flag vocabulary lives in
// app/featureflags, which this package does not import: the policy only needs
// the one scope a narrower permission reaches and the one an admin's own
// second factor lives under.
const (
	featureScopeUser  = "user"
	featureScopeAdmin = "admin"
)

// FeaturePolicy decides who may write a scope's flags. The two scopes are not
// the same risk: a user's flags are its rail vetoes, which an operator working
// a compliance case may need to set (features.user.update), while an admin's
// include admin-2fa-required, the second factor on their login — writing it is
// reaching into how another operator, or yourself, signs in, so it takes
// features.update. Which other admin one may act on is still
// AdminPolicy.MayActOn, asked by the service; one's own flags are this
// policy's: only a super writes them, because a non-super holding
// features.update would otherwise exempt their own login from the second
// factor. The self-service lever for one's own factor is PUT /me/totp.
type FeaturePolicy struct {
	admins *AdminPolicy
}

func NewFeaturePolicy(admins *AdminPolicy) *FeaturePolicy {
	return &FeaturePolicy{admins: admins}
}

// Update allows features.update on any scope, and features.user.update on the
// user scope alone. A scope with no rule of its own takes the wider
// permission; the service answers it 404.
func (p *FeaturePolicy) Update(_ context.Context, arguments map[string]any) contractsaccess.Response {
	scope, ok := arguments[ArgScope].(string)
	if !ok {
		return access.NewDenyResponse("no feature scope to authorize")
	}
	admin, ok := arguments[ArgAdmin].(*models.Admin)
	if !ok || admin == nil {
		return access.NewDenyResponse("forbidden")
	}
	if scope == featureScopeAdmin && isSelf(arguments[ArgTargetID], admin) {
		if super, err := p.admins.IsSuperAdmin(admin); err != nil || !super {
			return access.NewDenyResponse("an admin's own flags are written by a super")
		}
	}
	if p.admins.Can(admin, "features.update") {
		return access.NewAllowResponse()
	}
	if scope == featureScopeUser && p.admins.Can(admin, "features.user.update") {
		return access.NewAllowResponse()
	}
	return access.NewDenyResponse("forbidden")
}

// isSelf reports whether target names the acting admin.
func isSelf(target any, admin *models.Admin) bool {
	id, ok := target.(uint)
	return ok && id != 0 && id == admin.ID
}
