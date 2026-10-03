package keyexport

import (
	"errors"
	"fmt"
)

// RefusalError is why a wallet was not exported, worded to be safe to print: it may
// name wallets, chains and addresses, never keys, shares or passphrases.
type RefusalError struct {
	Reason string
}

func (e *RefusalError) Error() string { return e.Reason }

func refuse(format string, args ...any) error {
	return &RefusalError{Reason: fmt.Sprintf(format, args...)}
}

// refusalReason is the printable reason of err; errors that are not refusals are
// reported generically because their text may quote the input they failed on.
func refusalReason(err error) string {
	var refusal *RefusalError
	if errors.As(err, &refusal) {
		return refusal.Reason
	}
	return "internal error while reconstructing keys"
}
