package cms

import "github.com/goravel/framework/errors"

// ErrServiceProviderNotRegistered is what the facade panics with when the
// provider was never registered.
var ErrServiceProviderNotRegistered = errors.New("please register the cms service provider").SetModule(Name)
