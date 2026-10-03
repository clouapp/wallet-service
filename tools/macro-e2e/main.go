// Command macro-e2e holds the e2e helpers of the Wallets back end. It runs without
// booting Goravel on purpose: the app boot reads .env (the dev `vault` database), while
// these helpers target vault_test through <state dir>/wallets-api.environ, and `api`
// rebuilds and restarts the very binary an artisan command would run from.
//
//	macro-e2e capture-env [--dry-run]
//	macro-e2e api [--build] [--status] [--no-start] [--lock-wait SECONDS]
//	macro-e2e preflight withdrawal WALLET_ID ASSET AMOUNT_BASE_UNITS TO_ADDRESS
//	macro-e2e preflight consolidation WALLET_ID ASSET
//	macro-e2e send-from-base TAG WALLET_ID ASSET AMOUNT_BASE_UNITS DECIMALS TO EXTERNAL_USER_ID
//	    [--apply] [--recording-lock-held-by OWNER] [--chain CHAIN]
//	macro-e2e consolidate TAG WALLET_ID ASSET [--apply]
//	macro-e2e fee-estimate WALLET_ID ASSET TO_ADDRESS [--amount DECIMAL]
//
// Run from macro-wallets/back (go run ./tools/macro-e2e ...) or set MACRO_WALLETS_BACK_DIR.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/macrowallets/waas/pkg/e2evault"
	"github.com/macrowallets/waas/pkg/pyjson"
	"github.com/macrowallets/waas/tools/macro-e2e/e2e"
)

const (
	exitUsage    = 2
	privateUmask = 0o077
	usageText    = `usage:
  macro-e2e capture-env [--dry-run]
  macro-e2e api [--build] [--status] [--no-start] [--lock-wait SECONDS]
  macro-e2e preflight withdrawal WALLET_ID ASSET AMOUNT_BASE_UNITS TO_ADDRESS
  macro-e2e preflight consolidation WALLET_ID ASSET
  macro-e2e send-from-base TAG WALLET_ID ASSET AMOUNT_BASE_UNITS DECIMALS TO EXTERNAL_USER_ID [--apply] [--recording-lock-held-by OWNER] [--chain CHAIN]
  macro-e2e consolidate TAG WALLET_ID ASSET [--apply]
  macro-e2e fee-estimate WALLET_ID ASSET TO_ADDRESS [--amount DECIMAL]`
	jsonIndent = 2
)

func main() {
	syscall.Umask(privateUmask)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(errOut, usageText)
		return exitUsage
	}
	code, err := dispatch(ctx, args[0], args[1:], out, errOut)
	if errors.Is(err, e2e.ErrUsage) {
		fmt.Fprintf(errOut, "%v\n%s\n", err, usageText)
		return exitUsage
	}
	if err != nil {
		fmt.Fprintln(errOut, err)
		if code == e2e.ExitOK {
			return e2e.ExitFailure
		}
	}
	return code
}

func dispatch(ctx context.Context, command string, args []string, out, errOut io.Writer) (int, error) {
	switch command {
	case "capture-env":
		return captureEnv(args, out)
	case "api":
		return api(ctx, args, out, errOut)
	case "preflight":
		return preflight(ctx, args, out)
	case "send-from-base":
		return sendFromBase(ctx, args, out, errOut)
	case "consolidate":
		return consolidate(ctx, args, out, errOut)
	case "fee-estimate":
		return feeEstimate(ctx, args, out)
	case "help", "-h", "--help":
		fmt.Fprintln(out, usageText)
		return e2e.ExitOK, nil
	default:
		return exitUsage, fmt.Errorf("%w: unknown command %q", e2e.ErrUsage, command)
	}
}

func captureEnv(args []string, out io.Writer) (int, error) {
	parsed, err := e2e.ParseArgs(args, []string{"dry-run"}, nil, 0)
	if err != nil {
		return exitUsage, err
	}
	paths, err := e2e.DefaultPaths()
	if err != nil {
		return e2e.ExitFailure, err
	}
	return e2e.ExitOK, e2e.CaptureEnv(paths, parsed.Switches["dry-run"], time.Now(), out)
}

func api(ctx context.Context, args []string, out, errOut io.Writer) (int, error) {
	parsed, err := e2e.ParseArgs(args, []string{"build", "status", "no-start"}, []string{"lock-wait"}, 0)
	if err != nil {
		return exitUsage, err
	}
	lockWait := e2e.DefaultRestartLockWait
	if raw, present := parsed.Values["lock-wait"]; present {
		seconds, err := strconv.Atoi(raw)
		if err != nil || seconds <= 0 {
			return exitUsage, fmt.Errorf("%w: --lock-wait must be a positive number of seconds", e2e.ErrUsage)
		}
		lockWait = time.Duration(seconds) * time.Second
	}
	paths, err := e2e.DefaultPaths()
	if err != nil {
		return e2e.ExitFailure, err
	}
	manager := e2e.NewAPIManager(paths, recordingLock(paths), out, errOut)
	return e2e.ExitOK, manager.Run(ctx, e2e.APIOptions{
		Build:    parsed.Switches["build"],
		Status:   parsed.Switches["status"],
		NoStart:  parsed.Switches["no-start"],
		LockWait: lockWait,
	})
}

func preflight(ctx context.Context, args []string, out io.Writer) (int, error) {
	if len(args) == 0 {
		return exitUsage, fmt.Errorf("%w: preflight needs withdrawal or consolidation", e2e.ErrUsage)
	}
	request := e2e.PreflightRequest{Mode: args[0]}
	switch request.Mode {
	case e2e.PreflightModeWithdrawal:
		parsed, err := e2e.ParseArgs(args[1:], nil, nil, 4)
		if err != nil {
			return exitUsage, err
		}
		request.WalletID, request.Asset, request.Amount, request.To = parsed.Positional[0], parsed.Positional[1], parsed.Positional[2], parsed.Positional[3]
	case e2e.PreflightModeConsolidation:
		parsed, err := e2e.ParseArgs(args[1:], nil, nil, 2)
		if err != nil {
			return exitUsage, err
		}
		request.WalletID, request.Asset = parsed.Positional[0], parsed.Positional[1]
	default:
		return exitUsage, fmt.Errorf("%w: unknown pre-flight mode %q", e2e.ErrUsage, request.Mode)
	}
	runner, err := newPreflight()
	if err != nil {
		return e2e.ExitFailure, err
	}
	result, err := runner.Execute(ctx, request)
	if err != nil {
		return e2e.ExitFailure, err
	}
	indented, err := pyjson.DumpsIndent(result, jsonIndent)
	if err != nil {
		return e2e.ExitFailure, err
	}
	fmt.Fprintln(out, indented)
	return e2e.ExitOK, e2e.VerifyPreflight(result)
}

func sendFromBase(ctx context.Context, args []string, out, errOut io.Writer) (int, error) {
	parsed, err := e2e.ParseArgs(args, []string{"apply"}, []string{"recording-lock-held-by", "chain"}, 7)
	if err != nil {
		return exitUsage, err
	}
	funding, err := newFunding(out, errOut)
	if err != nil {
		return e2e.ExitFailure, err
	}
	positional := parsed.Positional
	return funding.SendFromBase(ctx, e2e.SendRequest{
		Tag:                 positional[0],
		WalletID:            positional[1],
		Asset:               positional[2],
		BaseUnits:           positional[3],
		Decimals:            positional[4],
		To:                  positional[5],
		ExternalUserID:      positional[6],
		Chain:               parsed.Values["chain"],
		RecordingLockHeldBy: parsed.Values["recording-lock-held-by"],
		Apply:               parsed.Switches["apply"],
	})
}

func consolidate(ctx context.Context, args []string, out, errOut io.Writer) (int, error) {
	parsed, err := e2e.ParseArgs(args, []string{"apply"}, nil, 3)
	if err != nil {
		return exitUsage, err
	}
	funding, err := newFunding(out, errOut)
	if err != nil {
		return e2e.ExitFailure, err
	}
	return funding.Consolidate(ctx, e2e.ConsolidateRequest{
		Tag:      parsed.Positional[0],
		WalletID: parsed.Positional[1],
		Asset:    parsed.Positional[2],
		Apply:    parsed.Switches["apply"],
	})
}

func feeEstimate(ctx context.Context, args []string, out io.Writer) (int, error) {
	parsed, err := e2e.ParseArgs(args, nil, []string{"amount"}, 3)
	if err != nil {
		return exitUsage, err
	}
	request := e2e.FeeEstimateRequest{WalletID: parsed.Positional[0], Asset: parsed.Positional[1], To: parsed.Positional[2], Amount: parsed.Values["amount"]}
	result, err := e2e.FeeEstimate(ctx, request, e2e.DockerServicesFromEnv().MarketsToken, e2e.APIClientFromEnv())
	if err != nil {
		return e2e.ExitFailure, err
	}
	indented, err := pyjson.DumpsIndent(result.Body, jsonIndent)
	if err != nil {
		return e2e.ExitFailure, err
	}
	fmt.Fprintf(out, "HTTP %d\n%s\n", result.Status, indented)
	if result.Status != http.StatusOK {
		return e2e.ExitFailure, nil
	}
	return e2e.ExitOK, nil
}

func newPreflight() (e2e.Preflight, error) {
	paths, err := e2e.DefaultPaths()
	if err != nil {
		return e2e.Preflight{}, err
	}
	vault, err := e2evault.DefaultVaultPassphrase()
	if err != nil {
		return e2e.Preflight{}, err
	}
	return e2e.Preflight{Paths: paths, Passphrases: vault, Run: e2e.ExecRunner, Timeout: e2e.PreflightTimeout}, nil
}

func newFunding(out, errOut io.Writer) (e2e.Funding, error) {
	runner, err := newPreflight()
	if err != nil {
		return e2e.Funding{}, err
	}
	docker := e2e.DockerServicesFromEnv()
	return e2e.Funding{
		Paths:       runner.Paths,
		Preflight:   runner.Verified,
		Passphrases: runner.Passphrases,
		Services: e2e.Services{
			OutboundMatches: docker.OutboundMatches,
			MarketsToken:    docker.MarketsToken,
			API:             e2e.APIClientFromEnv(),
		},
		Ledger:    e2e.FundingLedger{Path: runner.Paths.FundingLedger, Now: time.Now},
		Recording: recordingLock(runner.Paths),
		Now:       time.Now,
		Sleep:     e2e.SleepContext,
		PollEvery: e2e.TxHashPollInterval,
		PollFor:   e2e.TxHashWait,
		Out:       out,
		Err:       errOut,
	}, nil
}

func recordingLock(paths e2e.Paths) e2e.RecordingLock {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = ""
	}
	return e2e.RecordingLock{Path: paths.RecordingLock, PID: os.Getpid(), Hostname: hostname, Now: time.Now}
}
