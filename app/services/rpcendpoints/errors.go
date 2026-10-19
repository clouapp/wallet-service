package rpcendpoints

import (
	"errors"
	"fmt"
)

// ErrChainNotFound is a chain id that does not exist.
var ErrChainNotFound = errors.New("chain not found")

// Refusal is a change that is not made: a position out of range, a repeated
// endpoint, a disabled or unusable primary, a chain that does not fail over
// through this list, or an endpoint on another network or that did not answer.
// Field names what was refused (url, urls, position, order); nothing was written.
type Refusal struct {
	Field  string
	Reason string
}

func (r *Refusal) Error() string {
	if r == nil {
		return "refused"
	}
	return r.Reason
}

func refuse(field, format string, args ...any) error {
	return &Refusal{Field: field, Reason: fmt.Sprintf(format, args...)}
}
