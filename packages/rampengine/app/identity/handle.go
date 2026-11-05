package identity

import "errors"

// ErrHandleTaken refuses a handle another account already holds. Handles are
// one namespace whoever sets them — the user at sign-up or in their profile,
// or an admin — so every path refuses with this one value.
var ErrHandleTaken = errors.New("handle already taken")
