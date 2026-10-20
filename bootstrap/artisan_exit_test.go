package bootstrap

import (
	"errors"
	"fmt"
	"testing"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"
)

type stubCommand struct{ err error }

func (c stubCommand) Signature() string            { return "stub:run" }
func (c stubCommand) Description() string          { return "stub" }
func (c stubCommand) Extend() command.Extend       { return command.Extend{Category: "stub"} }
func (c stubCommand) Handle(console.Context) error { return c.err }

// Goravel panics with the error text when a command's Handle returns an error
// (console.Application.Run with exitIfArtisan), so a failing command would
// print a stack trace. The wrapper exits 1 instead and keeps everything else.
func TestExitOnFailure_ExitsOneWhenTheCommandFails(t *testing.T) {
	var codes []int
	wrapped := exitOnFailure(stubCommand{err: errors.New("boom")}, func(code int) { codes = append(codes, code) })

	if err := wrapped.Handle(nil); err != nil {
		t.Fatalf("Handle returned %v: the framework would panic on it", err)
	}
	if len(codes) != 1 || codes[0] != 1 {
		t.Fatalf("exit codes = %v, want [1]", codes)
	}
}

func TestExitOnFailure_LeavesASuccessfulCommandAlone(t *testing.T) {
	var codes []int
	wrapped := exitOnFailure(stubCommand{}, func(code int) { codes = append(codes, code) })

	if err := wrapped.Handle(nil); err != nil {
		t.Fatal(err)
	}
	if len(codes) != 0 {
		t.Fatalf("exit called with %v on success", codes)
	}
}

func TestExitOnFailure_KeepsTheCommandIdentity(t *testing.T) {
	wrapped := exitOnFailure(stubCommand{}, func(int) {})

	if wrapped.Signature() != "stub:run" || wrapped.Description() != "stub" || wrapped.Extend().Category != "stub" {
		t.Fatalf("wrapper changed the command: %s / %s / %+v", wrapped.Signature(), wrapped.Description(), wrapped.Extend())
	}
}

// codedError is a failure that names its exit status, as commands.exitStatus does.
type codedError struct{ code int }

func (e codedError) Error() string { return "unverified" }
func (e codedError) ExitCode() int { return e.code }

func TestExitOnFailure_ExitsWithTheStatusAFailureNames(t *testing.T) {
	var codes []int
	exit := func(code int) { codes = append(codes, code) }
	for _, err := range []error{codedError{code: 2}, fmt.Errorf("wrapped: %w", codedError{code: 3}), codedError{code: 0}} {
		if got := exitOnFailure(stubCommand{err: err}, exit).Handle(nil); got != nil {
			t.Fatal(got)
		}
	}
	if len(codes) != 3 || codes[0] != 2 || codes[1] != 3 || codes[2] != 1 {
		t.Fatalf("exit codes = %v, want [2 3 1]: a status below 2 is still a failure", codes)
	}
}
