package features

import "errors"

var (
	// ErrNotFound is a key that is not in the catalog. The route answers 404
	// before it answers 403.
	ErrNotFound = errors.New("feature not found")
	// ErrViewForbidden is a member who does not hold settings.view.
	ErrViewForbidden = errors.New("you do not have permission to view account features")
	// ErrUpdateForbidden is a member who does not hold settings.update.
	ErrUpdateForbidden = errors.New("you do not have permission to update account features")
	// ErrNotStored means the write returned without a row to read back.
	ErrNotStored = errors.New("feature flag was not stored")
)
