package bootstrap

import (
	"errors"
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
