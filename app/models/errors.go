package models

import "errors"

// ErrRepositoryNotFound is a missing row. Callers distinguish it from a
// database failure with errors.Is. A miss is never a nil pointer and a nil error.
var ErrRepositoryNotFound = errors.New("not found")

// ErrTransactionSettled is a transaction row another process settled (confirmed
// or failed) after the caller read it: a guarded write changed nothing.
var ErrTransactionSettled = errors.New("transaction already settled")
