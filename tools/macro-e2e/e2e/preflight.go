package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/pkg/pyjson"
)

const (
	PreflightModeWithdrawal    = "withdrawal"
	PreflightModeConsolidation = "consolidation"
	PreflightTimeout           = 300 * time.Second
	preflightResultPrefix      = "PREFLIGHT "
	preflightArtisanCommand    = "withdraw:preflight"
	artisanArgument            = "artisan"
	failureTailRunes           = 1500
	exitCodeUnknown            = -1
)

// PassphraseSource returns the verified wallet passphrase (the encrypted e2e wallet vault).
type PassphraseSource interface {
	Read(ctx context.Context, walletID uuid.UUID) (string, error)
}

// ProcessResult is the outcome of a finished child process.
type ProcessResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// ProcessRunner runs binary with args in dir, with exactly env, feeding stdin.
type ProcessRunner func(ctx context.Context, binary string, args []string, dir string, env []string, stdin []byte) (ProcessResult, error)

// PreflightRequest is one off-chain pre-flight: a withdrawal (amount, to) or a consolidation.
type PreflightRequest struct {
	Mode     string
	WalletID string
	Asset    string
	Amount   string
	To       string
}

// Validate applies the same patterns as the API inputs of the funding helpers.
func (request PreflightRequest) Validate() error {
	if request.Mode != PreflightModeWithdrawal && request.Mode != PreflightModeConsolidation {
		return fmt.Errorf("invalid pre-flight mode %s (want %s or %s)", pythonRepr(request.Mode), PreflightModeWithdrawal, PreflightModeConsolidation)
	}
	checks := []fieldCheck{{UUIDPattern, request.WalletID, "wallet id"}, {AssetPattern, request.Asset, "asset"}}
	if request.Mode == PreflightModeWithdrawal {
		checks = append(checks, fieldCheck{AmountPattern, request.Amount, "amount (base units)"}, fieldCheck{AddressPattern, request.To, "destination address"})
	}
	return requireAll(checks)
}

// PreflightFailure is a non-zero exit (or a missing PREFLIGHT line) of withdraw:preflight.
type PreflightFailure struct {
	ExitCode int
	Tail     string
}

func (failure *PreflightFailure) Error() string {
	return fmt.Sprintf("pre-flight failed (exit %d):\n%s", failure.ExitCode, failure.Tail)
}

// ErrPreflightUnverified: the signature was not verified or something was broadcast.
var ErrPreflightUnverified = errors.New("pre-flight did not verify the signature or reported a broadcast")

// Preflight plans, MPC-signs and verifies a transfer off-chain through
// `<state dir>/bin/waas-e2e-bin artisan withdraw:preflight`. Nothing is broadcast or
// persisted. The passphrase goes to the child on stdin only.
type Preflight struct {
	Paths       Paths
	Passphrases PassphraseSource
	Run         ProcessRunner
	Timeout     time.Duration
}

// Execute returns the PREFLIGHT result object (verify it with VerifyPreflight).
func (preflight Preflight) Execute(ctx context.Context, request PreflightRequest) (pyjson.Object, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	if preflight.Passphrases == nil || preflight.Run == nil {
		return nil, fmt.Errorf("pre-flight is not configured")
	}
	walletID, err := uuid.Parse(request.WalletID)
	if err != nil {
		return nil, fmt.Errorf("invalid wallet id: %s", pythonRepr(request.WalletID))
	}
	passphrase, err := preflight.Passphrases.Read(ctx, walletID)
	if err != nil {
		return nil, err
	}
	if info, err := os.Stat(preflight.Paths.APIBinary); err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is missing; run `macro-e2e api --build`", preflight.Paths.APIBinary)
	}
	environment, err := ReadAPIEnviron(preflight.Paths.APIEnviron)
	if err != nil {
		return nil, err
	}
	stdin, err := encodePreflightStdin(request, passphrase)
	if err != nil {
		return nil, err
	}
	defer zeroBytes(stdin)

	timeout := preflight.Timeout
	if timeout <= 0 {
		timeout = PreflightTimeout
	}
	runContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := preflight.Run(runContext, preflight.Paths.APIBinary, []string{artisanArgument, preflightArtisanCommand}, preflight.Paths.BackDir, EnvironList(environment), stdin)
	if errors.Is(runContext.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("pre-flight timed out after %s", timeout)
	}
	if err != nil {
		return nil, fmt.Errorf("run %s %s: %w", artisanArgument, preflightArtisanCommand, err)
	}
	return parsePreflightOutput(result)
}

func encodePreflightStdin(request PreflightRequest, passphrase string) ([]byte, error) {
	body := pyjson.Object{
		{Key: "mode", Value: request.Mode},
		{Key: "wallet_id", Value: request.WalletID},
		{Key: "asset", Value: request.Asset},
	}
	if request.Mode == PreflightModeWithdrawal {
		body = body.Set("amount", request.Amount).Set("to", request.To)
	}
	encoded, err := pyjson.Dumps(body.Set("passphrase", passphrase), pyjson.Default)
	if err != nil {
		return nil, err
	}
	return []byte(encoded), nil
}

func parsePreflightOutput(result ProcessResult) (pyjson.Object, error) {
	var lastResult string
	for _, line := range splitPythonLines(strings.ToValidUTF8(string(result.Stdout), "\uFFFD")) {
		if strings.HasPrefix(line, preflightResultPrefix) {
			lastResult = line
		}
	}
	if result.ExitCode != 0 || lastResult == "" {
		combined := strings.ToValidUTF8(string(result.Stdout)+string(result.Stderr), "\uFFFD")
		return nil, &PreflightFailure{ExitCode: result.ExitCode, Tail: lastRunes(combined, failureTailRunes)}
	}
	parsed, err := decodeObject([]byte(strings.TrimPrefix(lastResult, preflightResultPrefix)))
	if err != nil {
		return nil, fmt.Errorf("pre-flight result is not a JSON object: %w", err)
	}
	return parsed, nil
}

// VerifyPreflight requires signature_verified (truthy) and broadcast exactly false.
func VerifyPreflight(result pyjson.Object) error {
	verified, _ := result.Get("signature_verified")
	broadcast, present := result.Get("broadcast")
	if !pythonTruthy(verified) || !present || broadcast != false {
		return ErrPreflightUnverified
	}
	return nil
}

// Verified runs the pre-flight and requires VerifyPreflight; an unverified result is
// reported with its JSON, like the failure output of the stand-alone `preflight` command.
func (preflight Preflight) Verified(ctx context.Context, request PreflightRequest) (pyjson.Object, error) {
	result, err := preflight.Execute(ctx, request)
	if err != nil {
		return nil, err
	}
	if err := VerifyPreflight(result); err != nil {
		indented, encodeErr := pyjson.DumpsIndent(result, jsonIndent)
		if encodeErr != nil {
			return nil, err
		}
		return nil, fmt.Errorf("pre-flight failed:\n%s", lastRunes(indented+"\n"+err.Error(), failureTailRunes))
	}
	return result, nil
}

// PreflightTransactions returns the planned transactions (non-object items are kept as nil).
func PreflightTransactions(result pyjson.Object) []pyjson.Object {
	value, _ := result.Get("transactions")
	items, _ := value.([]any)
	transactions := make([]pyjson.Object, 0, len(items))
	for _, item := range items {
		object, _ := item.(pyjson.Object)
		transactions = append(transactions, object)
	}
	return transactions
}

func pythonTruthy(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case string:
		return typed != ""
	case []any:
		return len(typed) > 0
	case pyjson.Object:
		return len(typed) > 0
	case json.Number:
		number, err := typed.Float64()
		return err != nil || number != 0
	default:
		return true
	}
}

func splitPythonLines(text string) []string {
	return strings.Split(pythonLineBreaks.Replace(text), "\n")
}

// ExecRunner runs a real child process.
func ExecRunner(ctx context.Context, binary string, args []string, dir string, env []string, stdin []byte) (ProcessResult, error) {
	command := exec.CommandContext(ctx, binary, args...)
	command.Dir = dir
	command.Env = env
	command.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	result := ProcessResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), ExitCode: exitCodeUnknown}
	if command.ProcessState != nil {
		result.ExitCode = command.ProcessState.ExitCode()
	}
	var exitError *exec.ExitError
	if err != nil && !errors.As(err, &exitError) {
		return result, err
	}
	return result, nil
}

func zeroBytes(buffer []byte) {
	for index := range buffer {
		buffer[index] = 0
	}
}
