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
