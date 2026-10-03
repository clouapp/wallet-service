package settings

import "errors"

var (
	// ErrGroupNotFound is an unknown group name. The account route answers
	// 404 before it answers 403.
	ErrGroupNotFound = errors.New("settings group not found")
	// ErrSectionNotFound is an unknown settings page. The reset and cache routes
	// answer 404 before they answer 403, including a page that only holds platform groups.
	ErrSectionNotFound = errors.New("settings section not found")
	// ErrViewForbidden is a member who does not hold settings.view.
	ErrViewForbidden = errors.New("you do not have permission to view account settings")
	// ErrUpdateForbidden is a member who does not hold settings.update.
	ErrUpdateForbidden = errors.New("you do not have permission to update account settings")
	// ErrManagedByPlatform is an account-scoped group only platform staff may write.
	ErrManagedByPlatform = errors.New("platform staff manage this settings group")
	// errSealFailed is internal. The log names the key, never the plaintext.
	errSealFailed = errors.New("seal setting")
	// errServiceRequired is a nil settings service.
	errServiceRequired = errors.New("account settings: service is required")
)
