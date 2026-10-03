package users

import "errors"

var (
	// ErrPlatformForbidden is a caller who is not a platform admin. This
	// branch has no users.suspend permission catalog, so the gate is the
	// platform_admins row the other /v1/platform routes use.
	ErrPlatformForbidden = errors.New("you do not have permission to suspend users")
	// ErrNotFound is a user id that does not exist.
	ErrNotFound = errors.New("user not found")
)
