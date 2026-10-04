package settings

import "errors"

var (
	// ErrGroupNotFound is an unknown group name. The account route answers
	// 404 before it answers 403. The platform account route answers 404 for
	// an unknown name, a platform-only group, and an account-managed group
	// before it answers 403.
	ErrGroupNotFound = errors.New("settings group not found")
	// ErrAccountNotFound is an account id this platform route does not have.
	// It is 404 before the platform-admin check.
	ErrAccountNotFound = errors.New("account not found")
	// ErrSectionNotFound is an unknown settings page. The account reset and cache
	// routes answer 404 before they answer 403, including a page that only holds
	// platform groups. The platform cache and reset routes answer 404 before 403
	// for an unknown page, including a page that only holds account groups.
	ErrSectionNotFound = errors.New("settings section not found")
	// ErrViewForbidden is a member who does not hold settings.read.
	ErrViewForbidden = errors.New("you do not have permission to view account settings")
	// ErrUpdateForbidden is a member who does not hold settings.write.
	ErrUpdateForbidden = errors.New("you do not have permission to update account settings")
	// ErrManagedByPlatform is an account-scoped group only platform staff may write.
	ErrManagedByPlatform = errors.New("platform staff manage this settings group")
	// ErrPlatformForbidden is a caller who is not a platform admin. S1.4.4 names
	// settings.update; this branch has no platform permission catalog, so the
	// gate is the platform_admins row. sweep_limits names sweep.view and
	// sweep.update; a platform_admins row still stands in for that pair.
	// webhook_delivery, mail_smtp, and mail_delivery declare no permission
	// of their own.
	ErrPlatformForbidden = errors.New("you do not have permission to update settings")
	// ErrPlatformViewForbidden is a caller who is not a platform admin. S1.4.6
	// names settings.view for the platform index, for one platform group, and
	// for one platform-managed account group; this branch has no platform
	// permission catalog, so the gate is the platform_admins row. An unknown
	// group and an unknown account are 404 before this 403.
	ErrPlatformViewForbidden = errors.New("you do not have permission to view settings")
	// errSealFailed is internal. The log names the key, never the plaintext.
	errSealFailed = errors.New("seal setting")
	// errServiceRequired is a nil settings service.
	errServiceRequired = errors.New("account settings: service is required")
	// errSettingsCacheUnavailable is a missing process cache. The read falls
	// through to the database.
	errSettingsCacheUnavailable = errors.New("settings cache is not available")
)
