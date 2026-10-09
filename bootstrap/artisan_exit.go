package bootstrap

import (
	"os"

	"github.com/goravel/framework/contracts/console"
)

// exitingCommand ends the process with status 1 when its command fails.
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

func (c exitingCommand) Handle(ctx console.Context) error {
	if err := c.Command.Handle(ctx); err != nil {
		c.exit(1)
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
