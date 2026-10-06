package commands

import "github.com/goravel/framework/contracts/console"

// fail is the one way an artisan command reports a failure. It prints a
// redacted line and returns the error so the artisan runner exits non-zero.
func fail(ctx console.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx != nil {
		ctx.Error(redactedLine(err.Error()))
	}
	return redactedError(err)
}
