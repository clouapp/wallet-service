package commands

import (
	"errors"

	"github.com/macrowallets/waas/app/services/security"
)

// redactedLine is artisan console text. ctx.Error prints the string as given,
// and an error from an RPC or price client can carry the endpoint, including a key.
func redactedLine(text string) string {
	return security.RedactText(text)
}

// redactedError is the error artisan prints after a command returns. The
// original error stays when its text has nothing to hide, so errors.Is keeps
// working; a credential is replaced before that print.
func redactedError(err error) error {
	if err == nil {
		return nil
	}
	redacted := redactedLine(err.Error())
	if redacted == err.Error() {
		return err
	}
	return errors.New(redacted)
}
