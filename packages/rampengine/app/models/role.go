package models

import "github.com/goravel/framework/database/orm"

// RoleMorphType is the canonical model_type string for the `roles` table
// when referenced from polymorphic pivots.
const RoleMorphType = "roles"

// SuperAdminRoleName is the role whose membership grants every admin
// permission. Membership in it is the single source of truth for "is this
// admin a super" — there is no column mirroring it.
const SuperAdminRoleName = "super_admin"

// ComplianceRoleName is the role that holds full control over end users, their
// KYC and their provider customers. It is not a bypass: it carries its grants
// like any other role, and an operator may edit them in the panel.
const ComplianceRoleName = "compliance"

// CustomerGuard is the guard of the roles a customer's members hold. It is an
// RBAC guard only, never an auth guard: a membership does not log in.
const CustomerGuard = "customer"

// CustomerOwnerRoleName is the role every member holds today: the one who
// registered the customer and acts for it. It carries no permission yet —
// being a member is what a route checks until a member can hold less.
const CustomerOwnerRoleName = "owner"

// RBACSubjectTypes is every subject_type the RBAC pivots accept, in the order
// their CHECK constraints spell it. A polymorphic column carries no foreign
// key, so this list is what keeps a typo out of model_has_roles and
// model_has_permissions. A user holds no role of its own; its memberships do.
var RBACSubjectTypes = []string{AdminMorphType, CustomerMemberMorphType}

// Role groups Permissions and is assignable to any subject (admin in v1).
type Role struct {
	orm.Model
	Name        string `gorm:"not null" json:"name"`
	Guard       string `gorm:"not null;default:admin" json:"guard"`
	Description string `json:"description,omitempty"`
}
