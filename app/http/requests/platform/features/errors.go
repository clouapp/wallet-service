package features

import "errors"

const maxBodyBytes = 4096

var (
	// ErrBodyInvalid is a body that is not the JSON shape the route accepts.
	ErrBodyInvalid = errors.New("invalid request body")
	// ErrBodyTooLarge is a body over maxBodyBytes.
	ErrBodyTooLarge = errors.New("request body is too large")
	// ErrEnabledRequired is a single flag write that omits enabled.
	ErrEnabledRequired = errors.New("enabled is required")
	// ErrFeaturesRequired is a bulk write that omits features or sends an
	// empty list.
	ErrFeaturesRequired = errors.New("features is required")
	// ErrDuplicateKey is the same key twice in one bulk write.
	ErrDuplicateKey = errors.New("feature key is duplicated")
)
