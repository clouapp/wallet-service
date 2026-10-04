package policies

import (
	"errors"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

// PermUsersRead is the account member list. Owner, admin and auditor hold it.
// The user role does not: the S3.4.2 catalog gives that role no users grant.
const PermUsersRead = "users.read"

// Grants is the permission set for one request. Nil and empty fail closed.
// This branch keeps the set in code. There is no account_role_permissions row.
type Grants map[string]struct{}

// Can reports whether grants hold perm. A nil or empty set, and an empty
// permission, are false.
func Can(grants Grants, perm string) bool {
	if perm == "" || len(grants) == 0 {
		return false
	}
	_, ok := grants[perm]
	return ok
}

// AccountRoleGrants is the code catalog Can reads. The set records users.read
// for owner, admin and auditor. Other permissions stay on their existing
// policy functions. The retired viewer label uses the auditor set. Any other
// role, including user, gets an empty set.
func AccountRoleGrants(role string) Grants {
	if role == models.RetiredAccountRoleViewer {
		role = roleAuditor
	}
	switch role {
	case roleOwner, roleAdmin, roleAuditor:
		return Grants{PermUsersRead: {}}
	default:
		return nil
	}
}

var (
	ErrRoleAbove         = errors.New("cannot grant a role above your own")
	ErrCannotActOnMember = errors.New("cannot act on a member above your role")
	ErrCannotRemoveSelf  = errors.New("cannot remove yourself")
	ErrLastOwner         = errors.New("cannot remove the last owner")
)

// RefuseMemberRemoval applies rank, the ban on removing yourself, and last-owner protection.
// A nil error means the removal may proceed. activeOwners counts owners before the removal.
func RefuseMemberRemoval(actorID, targetID uuid.UUID, actorRole, targetRole string, activeOwners int) error {
	if actorID == uuid.Nil || targetID == uuid.Nil {
		return ErrCannotActOnMember
	}
	if actorID == targetID {
		return ErrCannotRemoveSelf
	}
	if !MayActOn(actorRole, targetRole) {
		return ErrCannotActOnMember
	}
	if targetRole == models.AccountRoleOwner && activeOwners <= 1 {
		return ErrLastOwner
	}
	return nil
}
