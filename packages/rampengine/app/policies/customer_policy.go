package policies

import (
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
)

// CustomerPolicy is the only reader of RBAC on the client side, as AdminPolicy
// is on the admin's (spec §4). It holds RolesOf alone: no route checks a
// permission yet — being a member is enough while every member is an owner —
// and Can arrives with the permission catalog, when a member can hold less.
type CustomerPolicy struct {
	roleRepo repositories.RoleRepository
}

func NewCustomerPolicy(roleRepo repositories.RoleRepository) *CustomerPolicy {
	return &CustomerPolicy{roleRepo: roleRepo}
}

// RolesOf returns the names of the roles the membership holds.
func (p *CustomerPolicy) RolesOf(member *models.CustomerMember) ([]string, error) {
	roles, err := p.roleRepo.RolesForSubject(member)
	if err != nil {
		return nil, err
	}
	return roleNames(roles), nil
}

// RolesOfMembers is RolesOf for many memberships in one read, keyed by
// membership id: what a listing of customers shows. A membership holding no
// role is absent.
func (p *CustomerPolicy) RolesOfMembers(members []models.CustomerMember) (map[uint][]string, error) {
	ids := make([]uint, 0, len(members))
	for i := range members {
		ids = append(ids, members[i].ID)
	}
	roles, err := p.roleRepo.RolesForSubjectIDs(models.CustomerMemberMorphType, ids)
	if err != nil {
		return nil, err
	}
	names := make(map[uint][]string, len(roles))
	for id, held := range roles {
		names[id] = roleNames(held)
	}
	return names, nil
}
