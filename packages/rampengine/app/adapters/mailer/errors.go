package mailer

import "errors"

// ErrUnknownDriver is returned for a driver with no transport behind it. There
// is no next provider to fall through to, so this is an error and never a skip.
var ErrUnknownDriver = errors.New("unknown mail driver")
