package policies

import (
	"errors"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

var (
	ErrRoleAbove         = errors.New("cannot grant a role above your own")
	ErrCannotActOnMember = errors.New("cannot act on a member above your role")
	ErrCannotRemoveSelf  = errors.New("cannot remove yourself")
	ErrLastOwner         = errors.New("cannot remove the last owner")
)

// MayGrant reports whether actorRole may hand out grantedRole.
// Nobody grants above their own rank. Equal ranks may grant each other:
// an admin may grant an admin, and user and auditor are the same rank.
func MayGrant(actorRole, grantedRole string) bool {
	if !models.IsAccountRole(actorRole) || !models.IsAccountRole(grantedRole) {
		return false
	}
	return !models.AccountRoleOutranks(grantedRole, actorRole)
}

// MayActOn reports whether actorRole may change or remove a member who holds targetRole.
// It is the same comparison as MayGrant, asked about the member who already has the role.
func MayActOn(actorRole, targetRole string) bool {
	if !models.IsAccountRole(actorRole) || !models.IsAccountRole(targetRole) {
		return false
	}
	return !models.AccountRoleOutranks(targetRole, actorRole)
}

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
