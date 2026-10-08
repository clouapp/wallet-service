package snapshot

import (
	"context"
)

// Command names accepted by RunCommand.
const (
	CommandExport  = "export"
	CommandRestore = "restore"
	CommandVerify  = "verify"
	CommandLoop    = "loop"
)

// RunCommand runs one CLI command and maps the result to the tool's exit codes:
// 0 ok, 1 failed, 3 export refused because restore has not run in this container.
func RunCommand(ctx context.Context, tool *Tool, log Logger, command string, verifyAll bool) int {
	var err error
	switch command {
	case CommandExport:
		var outcome ExportOutcome
		outcome, err = tool.Export(ctx)
		if err == nil && outcome == ExportRefused {
			return ExitRefused
		}
		if err == nil && outcome == ExportUnchanged {
			log.Printf("export: unchanged (live digest matches the newest snapshot)")
		}
	case CommandRestore:
		err = tool.Restore(ctx)
	case CommandVerify:
		err = tool.Verify(ctx, verifyAll)
	case CommandLoop:
		err = tool.Loop(ctx)
	default:
		log.Printf("unknown command %s", command)
		return ExitUsage
	}
	if err != nil {
		log.Printf("%s failed: %s", command, safeMessage(err))
		return ExitFailed
	}
	return ExitOK
}
