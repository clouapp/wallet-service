package features

import (
	"errors"

	"github.com/macrowallets/waas/app/policies"
)

var (
	// ErrNotFound is a key that is not in the catalog. The route answers 404
	// before it answers 403.
	ErrNotFound = errors.New("feature not found")
	// ErrScopeNotFound is a scope this catalog does not address by id.
	// S2.4 names account, user, and chain, and refuses global. This catalog
	// stores an account row and a global row. user, chain, and global are
	// 404 before the admin check.
	ErrScopeNotFound = errors.New("feature scope not found")
	// ErrInvalidAccountID is an account target that is not a UUID.
	ErrInvalidAccountID = errors.New("invalid account id")
	// ErrAccountNotFound is an account id that is not stored.
	ErrAccountNotFound = errors.New("account not found")
	// ErrDuplicateWrite is the same catalog key twice in one scoped write.
	// Nothing is stored.
	ErrDuplicateWrite = errors.New("feature key is duplicated")
	// ErrViewForbidden is a member who does not hold settings.view.
	ErrViewForbidden = errors.New(policies.MsgFeaturesViewDenied)
	// ErrUpdateForbidden is a member who does not hold settings.update.
	ErrUpdateForbidden = errors.New("you do not have permission to update account features")
	// ErrNotStored means the write returned without a row to read back.
	ErrNotStored = errors.New("feature flag was not stored")
	// ErrPlatformForbidden is a caller who is not a platform admin. Account
	// ownership does not grant this.
	ErrPlatformForbidden = errors.New("you do not have permission to manage platform features")
)
