package policies

import (
	"slices"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
)

// AdminPolicy is the sole authorization arbiter for the admin namespace.
// All permission checks (middleware, controllers) must go through Can.
type AdminPolicy struct {
	permRepo repositories.PermissionRepository
	roleRepo repositories.RoleRepository
}

func NewAdminPolicy(permRepo repositories.PermissionRepository, roleRepo repositories.RoleRepository) *AdminPolicy {
	return &AdminPolicy{permRepo: permRepo, roleRepo: roleRepo}
}

// IsSuperAdmin reports whether the admin holds super_admin, which is the only
// thing that grants the bypass.
func (p *AdminPolicy) IsSuperAdmin(admin *models.Admin) (bool, error) {
	if admin == nil {
		return false, nil
	}
	return p.roleRepo.SubjectHasRole(admin, models.SuperAdminRoleName, "admin")
}

// hasSuperRole denies and logs on a lookup error: the bypass must never be
// handed out by accident of the database being down.
func (p *AdminPolicy) hasSuperRole(admin *models.Admin) bool {
	ok, err := p.IsSuperAdmin(admin)
	if err != nil {
		facades.Log().Errorf("admin policy: super lookup: %v", err)
		return false
	}
	return ok
}

// Can returns true if the admin has the named permission. Holding the
// super_admin role short-circuits. On repo errors the policy denies and logs.
func (p *AdminPolicy) Can(admin *models.Admin, permission string) bool {
	if admin == nil {
		return false
	}
	if p.hasSuperRole(admin) {
		return true
	}
	ok, err := p.permRepo.SubjectHasPermission(admin, permission)
	if err != nil {
		facades.Log().Errorf("admin policy: %v", err)
		return false
	}
	return ok
}

// PermissionsOf returns the union of role-derived and direct permissions for
// the admin. Holding super_admin returns the entire catalog.
func (p *AdminPolicy) PermissionsOf(admin *models.Admin) ([]string, error) {
	if admin == nil {
		return nil, nil
	}
	if p.hasSuperRole(admin) {
		return p.permRepo.AllNames("admin")
	}
	return p.permRepo.PermissionsForSubject(admin)
}

// MayActOn reports whether requester may write target's account or the flags
// guarding its login, a way into it: a super may act on anyone, a non-super
// never on a super and otherwise only on an admin whose every permission they hold.
func (p *AdminPolicy) MayActOn(requester, target *models.Admin) (bool, error) {
	if super, err := p.IsSuperAdmin(requester); err != nil || super {
		return super, err
	}
	if super, err := p.IsSuperAdmin(target); err != nil || super {
		return false, err
	}
	held, err := p.permRepo.PermissionsForSubject(requester)
	if err != nil {
		return false, err
	}
	needed, err := p.permRepo.PermissionsForSubject(target)
	if err != nil {
		return false, err
	}
	for _, name := range needed {
		if !slices.Contains(held, name) {
			return false, nil
		}
	}
	return true, nil
}

// RolesOf returns the names of the roles the admin holds.
func (p *AdminPolicy) RolesOf(admin *models.Admin) ([]string, error) {
	roles, err := p.roleRepo.RolesForSubject(admin)
	if err != nil {
		return nil, err
	}
	return roleNames(roles), nil
}

// roleNames is what a policy's RolesOf answers: the roles' names, in order.
func roleNames(roles []models.Role) []string {
	names := make([]string, 0, len(roles))
	for _, role := range roles {
		names = append(names, role.Name)
	}
	return names
}
