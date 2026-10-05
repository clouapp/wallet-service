package policies

import (
	"errors"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

// PermUsersRead is the account member list. Owner, admin and auditor hold it.
// The user role does not: the S3.4.2 catalog gives that role no users grant.
const PermUsersRead = models.AccountPermUsersRead

// PermUsersWrite is adding or removing an account member and creating an account invite.
// Owner and admin hold it. Auditor and user do not.
const PermUsersWrite = models.AccountPermUsersWrite

// PermRolesRead is the account role catalog. Owner, admin and auditor hold
// it. The user role does not. There is no roles.write grant: this branch
// has no per-account permission override.
const PermRolesRead = models.AccountPermRolesRead

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

// AccountRoleGrants is the code catalog Can reads. Owner and admin hold
// users.read, users.write, settings.read, settings.write, roles.read,
// addresses.create, and account.write. Owner also holds account.lifecycle,
// the freeze and archive grant. Admin does not. The user role holds only
// addresses.create. Auditor holds users.read, settings.read and roles.read.
// The retired viewer label uses the auditor set. Any other role gets an
// empty set. Withdraw, sweep, and wallet create stay out of this set; those
// routes keep MayPerformFundAction.
func AccountRoleGrants(role string) Grants {
	if role == models.RetiredAccountRoleViewer {
		role = roleAuditor
	}
	switch role {
	case roleOwner:
		grants := ownerAdminAccountGrants()
		grants[PermAccountLifecycle] = struct{}{}
		return grants
	case roleAdmin:
		return ownerAdminAccountGrants()
	case roleUser:
		return Grants{
			PermAddressesCreate: {},
		}
	case roleAuditor:
		return Grants{
			PermUsersRead:    {},
			PermSettingsRead: {},
			PermRolesRead:    {},
		}
	default:
		return nil
	}
}

// ownerAdminAccountGrants is the shared account catalog for owner and admin.
// Freeze and archive are not in this set: only the owner grant adds account.lifecycle.
func ownerAdminAccountGrants() Grants {
	return Grants{
		PermUsersRead:       {},
		PermUsersWrite:      {},
		PermSettingsRead:    {},
		PermSettingsWrite:   {},
		PermRolesRead:       {},
		PermAddressesCreate: {},
		PermAccountWrite:    {},
	}
}

// WalletGrants is the code catalog WalletCan reads. Owner, admin and user may
// generate an address. Auditor is read-only, and a stored viewer follows the
// auditor. Withdraw, sweep and wallet create stay out of this set so the user
// role cannot move funds; those routes keep MayPerformFundAction.
func WalletGrants(role string) Grants {
	if role == models.RetiredAccountRoleViewer {
		role = roleAuditor
	}
	switch role {
	case roleOwner, roleAdmin, roleUser:
		return Grants{PermAddressesCreate: {}}
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
