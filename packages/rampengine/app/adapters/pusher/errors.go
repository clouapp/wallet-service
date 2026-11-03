package pusher

import "errors"

// ErrUnknownPlatform is returned for a platform with no transport behind it.
var ErrUnknownPlatform = errors.New("unknown push platform")

// ErrUnregistered is a device token the vendor will never deliver to again:
// the app was uninstalled, the token was rotated, or it was issued for another
// app or APNs environment. The caller forgets the token; retrying cannot help.
var ErrUnregistered = errors.New("device token is no longer registered")

// ErrNoCredentials is a device whose environment this installation holds no
// credentials for: an APNs key reaches one environment unless it is team
// scoped, and an operator may configure only the one their builds use. No
// retry changes that; the delivery is dropped, not failed.
var ErrNoCredentials = errors.New("no push credentials for the device's environment")
