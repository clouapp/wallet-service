// Command secrets-snapshot keeps encrypted Secrets Manager snapshots for the
// community LocalStack image, which has no native persistence: MPC share B
// (vault/wallet/<id>/share-b) would vanish on every container restart. It runs
// inside the waas-localstack container as a static binary (the image has no Go):
//
//	export            snapshot every secret to /var/lib/localstack/secrets-snapshot (gpg, atomic, rotated)
//	export --during-shutdown
//	                  same, marking requests as LocalStack-internal so the gateway still
//	                  serves them while the runtime shuts down (shutdown.d hook)
//	restore           recreate secrets missing from LocalStack from the newest readable
//	                  snapshot; never overwrites, keeps the original ARN suffix
//	verify [--all]    read-only dry run of restore: decrypt, check meta, compare with live
//	loop              export every SECRETS_SNAPSHOT_INTERVAL seconds (ready.d hook)
//
// Snapshots only grow: a secret missing live is carried over from the previous
// snapshot unless SECRETS_SNAPSHOT_FORCE=1. Only names and counts are logged.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/macrowallets/waas/tools/localstack-secrets-snapshot/snapshot"
)

const (
	privateUmask         = 0o077
	duringShutdownFlag   = "--during-shutdown"
	verifyAllSnapshots   = "--all"
	usage                = "usage: secrets-snapshot export [--during-shutdown] | restore | verify [--all] | loop"
	maxArgumentsExpected = 2
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	syscall.Umask(privateUmask)
	logger := snapshot.StampedLogger{Out: os.Stdout}
	if len(args) == 0 || len(args) > maxArgumentsExpected {
		fmt.Fprintln(os.Stderr, usage)
		return snapshot.ExitUsage
	}
	command, option := args[0], ""
	if len(args) == maxArgumentsExpected {
		option = args[1]
	}
	if !validInvocation(command, option) {
		fmt.Fprintln(os.Stderr, usage)
		return snapshot.ExitUsage
	}

	cfg, err := snapshot.ConfigFromEnv()
	if err != nil {
		logger.Printf("%s failed: %v", command, err)
		return snapshot.ExitFailed
	}
	internal := command == snapshot.CommandExport && option == duringShutdownFlag
	tool, err := snapshot.NewTool(cfg, snapshot.Dependencies{
		Secrets: snapshot.NewAWSSecrets(cfg, internal),
		Crypter: snapshot.GPGCrypter{KeyFile: cfg.KeyFile},
		Log:     logger,
	})
	if err != nil {
		logger.Printf("%s failed: %v", command, err)
		return snapshot.ExitFailed
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return snapshot.RunCommand(ctx, tool, logger, command, option == verifyAllSnapshots)
}

func validInvocation(command, option string) bool {
	switch command {
	case snapshot.CommandExport:
		return option == "" || option == duringShutdownFlag
	case snapshot.CommandVerify:
		return option == "" || option == verifyAllSnapshots
	case snapshot.CommandRestore, snapshot.CommandLoop:
		return option == ""
	default:
		return false
	}
}
