package policies

import (
	"context"

	"github.com/goravel/framework/auth/access"
	contractsaccess "github.com/goravel/framework/contracts/auth/access"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/settings"
)

// Abilities the settings policy answers, registered with the Gate.
const (
	ViewSettingsGroup   = "view-settings-group"
	UpdateSettingsGroup = "update-settings-group"
)

// The arguments those abilities read. The acting admin is one of them, not a
// context value: see "The actor" in .ai/guidelines/authorization.md.
const (
	ArgGroup = "group"
	ArgAdmin = "admin"
)

// SettingsPolicy decides access to a settings group. A group may name its own
// permission, required on top of the settings.* pair the route middleware
// checks: an operator trusted with the SMTP password is not thereby trusted
// with a live API key.
type SettingsPolicy struct {
	admins *AdminPolicy
}

func NewSettingsPolicy(admins *AdminPolicy) *SettingsPolicy {
	return &SettingsPolicy{admins: admins}
}

func (p *SettingsPolicy) View(_ context.Context, arguments map[string]any) contractsaccess.Response {
	return p.check(arguments, func(g settings.Group) string { return g.ViewPermission })
}

func (p *SettingsPolicy) Update(_ context.Context, arguments map[string]any) contractsaccess.Response {
	return p.check(arguments, func(g settings.Group) string { return g.UpdatePermission })
}

func (p *SettingsPolicy) check(arguments map[string]any, permissionOf func(settings.Group) string) contractsaccess.Response {
	group, ok := arguments[ArgGroup].(settings.Group)
	if !ok {
		return access.NewDenyResponse("no settings group to authorize")
	}
	permission := permissionOf(group)
	if permission == "" {
		return access.NewAllowResponse()
	}
	admin, ok := arguments[ArgAdmin].(*models.Admin)
	if !ok || !p.admins.Can(admin, permission) {
		return access.NewDenyResponse("forbidden")
	}
	return access.NewAllowResponse()
}
