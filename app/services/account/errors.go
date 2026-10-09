package account

import (
	"errors"

	"github.com/macrowallets/waas/app/policies"
)

var (
	// ErrMemberNotFound is a membership that is not on the account.
	ErrMemberNotFound = errors.New("member not found")
	// ErrSelfMembership is a caller changing their own role or status, or removing themselves.
	ErrSelfMembership = errors.New("cannot change your own membership")
	// ErrManageMembers is a caller who is not an owner or admin.
	ErrManageMembers = errors.New(policies.MsgManageMembers)
	// ErrGrantRole is a role above the caller's rank.
	ErrGrantRole = errors.New("cannot grant a role above your own")
	// ErrActOnMember is a target whose current role is above the caller's rank.
	ErrActOnMember = errors.New("cannot change a member above your rank")
	// ErrLastOwner is a suspend, demotion or removal that would leave the account with no owner.
	ErrLastOwner = errors.New("cannot remove or suspend the last owner")
	// ErrAlreadyMember is AddUser's refusal of a user whose membership is on the
	// account already (active or suspended); a removed one is restored instead.
	ErrAlreadyMember = errors.New("user is already a member of this account")
	// ErrMemberRole is a role outside owner, admin, auditor and user.
	ErrMemberRole = errors.New("invalid member role")
	// ErrMemberStatus is a status outside active and suspended.
	ErrMemberStatus = errors.New("invalid member status")
	// ErrMemberChangeEmpty is a change that sets neither role nor status.
	ErrMemberChangeEmpty = errors.New("role or status is required")
	// ErrAccessTokenNotFound is a token that is missing or belongs to another account.
	ErrAccessTokenNotFound = errors.New("token not found")
	// ErrPlatformLifecycleForbidden is a caller who is not a platform admin.
	// S3.4.1 names accounts.lifecycle. This branch has no platform permission
	// catalog, so a platform_admins row is the gate.
	ErrPlatformLifecycleForbidden = errors.New("you do not have permission to change account status")
	// ErrAccountNotFound is an account id that does not exist.
	ErrAccountNotFound = errors.New("account not found")
	// ErrAccountStatus is a lifecycle status outside active, frozen, and archived.
	ErrAccountStatus = errors.New("invalid account status")
	// ErrPlatformViewForbidden is a caller who is not a platform admin.
	// S3.4.1 names accounts.view. This branch has no platform permission
	// catalog, so a platform_admins row is the gate.
	ErrPlatformViewForbidden = errors.New("you do not have permission to view accounts")
	// ErrPlatformAccountUsersForbidden is a caller who is not a platform admin.
	// S3.4.1 names GET /{id}/users on /v1/platform/accounts. This branch has no
	// platform permission catalog, so a platform_admins row is the gate.
	ErrPlatformAccountUsersForbidden = errors.New("you do not have permission to view account users")
	// ErrPlatformOwnersForbidden is a caller who is not a platform admin.
	// S3.4.1 names POST /{id}/owners (attach owner — recovery) accounts.owners.
	// This branch has no platform permission catalog, so a platform_admins row
	// is the gate.
	ErrPlatformOwnersForbidden = errors.New("you do not have permission to attach an account owner")
	// ErrPlatformOwnerUserNotFound is an email that is not an existing user.
	// Attach does not create a user.
	ErrPlatformOwnerUserNotFound = errors.New("user not found")
)
