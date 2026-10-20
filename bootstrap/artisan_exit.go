package bootstrap

import (
	"errors"
	"os"

	"github.com/goravel/framework/contracts/console"
)

// exitingCommand ends the process with status 1 when its command fails, or with the
// status the failure names (ExitCode, 2 or more).
//
// Goravel runs an artisan command inside Create() and, when Handle returns an
// error, panics with the error text (console.Application.Run, exitIfArtisan):
// a stack trace on stderr for what is only a failed command. Commands print
// their own redacted failure line (commands.fail) and return the error, so the
// wrapper only turns the return into an exit code. Commands the framework
// ships (migrate, ...) are not wrapped and keep the framework's behaviour.
type exitingCommand struct {
	console.Command
	exit func(code int)
}

func exitOnFailure(cmd console.Command, exit func(code int)) console.Command {
	return exitingCommand{Command: cmd, exit: exit}
}

// exitCoder is a failure that names its exit status (commands.exitStatus).
type exitCoder interface {
	ExitCode() int
}

func (c exitingCommand) Handle(ctx console.Context) error {
	if err := c.Command.Handle(ctx); err != nil {
		code := 1
		var coded exitCoder
		if errors.As(err, &coded) && coded.ExitCode() > 1 {
			code = coded.ExitCode()
		}
		c.exit(code)
	}
	return nil
}

// exitOnFailures wraps every command so that a failure exits with status 1.
func exitOnFailures(cmds []console.Command) []console.Command {
	wrapped := make([]console.Command, len(cmds))
	for i, cmd := range cmds {
		wrapped[i] = exitOnFailure(cmd, os.Exit)
	}
	return wrapped
}
