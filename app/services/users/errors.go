package users

import "errors"

var (
	// ErrPlatformForbidden is a caller who is not a platform admin. This
	// branch has no users.suspend permission catalog, so the gate is the
	// platform_admins row the other /v1/platform routes use.
	ErrPlatformForbidden = errors.New("you do not have permission to suspend users")
	// ErrNotFound is a user id that does not exist.
	ErrNotFound = errors.New("user not found")
	// ErrSessionsForbidden is a caller who is not a platform admin. S3.4.1
	// names users.sessions.revoke; this branch has no platform permission
	// catalog, so the gate is the platform_admins row.
	ErrSessionsForbidden = errors.New("you do not have permission to revoke user sessions")
	// ErrMFAForbidden is a caller who is not a platform admin. S3.4.1 names
	// users.mfa.reset; this branch has no platform permission catalog, so the
	// gate is the platform_admins row.
	ErrMFAForbidden = errors.New("you do not have permission to reset user mfa")
)
