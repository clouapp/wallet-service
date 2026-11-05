package identity

import "errors"

// ErrEmailTaken refuses an address another account of the same table holds.
// The service's availability read and the unique index both answer with it: the
// index settles two requests that passed the read together.
var ErrEmailTaken = errors.New("email already taken")
