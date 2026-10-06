package evmcall

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

const (
	// ClaimDirEnv names the directory of claim files when --claim-dir is empty.
	ClaimDirEnv = "EVM_CALL_CLAIM_DIR"
	// StateDirEnv is the e2e state directory whose locks subdirectory holds claims.
	StateDirEnv     = "MACRO_E2E_STATE_DIR"
	defaultStateDir = ".local/state/macro-e2e"
	locksSubdir     = "locks"
)

// RunRequest is one evm:call invocation after the flags have been parsed.
type RunRequest struct {
	Request         Request
	RPCEnv          string
	Broadcast       bool
	PassphraseStdin bool
	ClaimDir        string
}

// Runner simulates or broadcasts one EVM call. The passphrase is read here
// and is never returned.
type Runner struct {
	Wallets WalletSource
	Signer  Signer
}

// Execute simulates, or with Broadcast signs and sends once.
func (r *Runner) Execute(parent context.Context, req RunRequest) ([]string, error) {
	if r == nil || r.Wallets == nil {
		return nil, fmt.Errorf("evm call: wallet source is required")
	}
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	service, err := r.service(req)
	if err != nil {
		return nil, err
	}
	if !req.Broadcast {
		plan, err := service.Simulate(ctx, req.Request)
		if err != nil {
			return nil, err
		}
		return []string{
			"plan " + mustJSON(plan),
			fmt.Sprintf("dry run (rpc from $%s): simulated and estimated; nothing signed or sent", req.RPCEnv),
		}, nil
	}
	passphrase, err := runPassphrase(ctx, req)
	if err != nil {
		return nil, err
	}
	result, err := service.Broadcast(ctx, req.Request, passphrase)
	var lines []string
	if result != nil {
		lines = append(lines, "result "+mustJSON(result))
	}
	if err != nil {
		return lines, err
	}
	if result.Outcome != OutcomeReceiptSuccess {
		return lines, fmt.Errorf("transaction %s: %s", result.TxHash, result.Outcome)
	}
	lines = append(lines, fmt.Sprintf("tx %s succeeded in block %d", result.TxHash, result.Receipt.BlockNumber))
	return lines, nil
}

func (r *Runner) service(req RunRequest) (*Service, error) {
	rpcURL := strings.TrimSpace(os.Getenv(req.RPCEnv))
	if rpcURL == "" {
		return nil, fmt.Errorf("environment variable %s is not set", req.RPCEnv)
	}
	rpc, err := NewJSONRPC(rpcURL)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", req.RPCEnv, err)
	}
	deps := Dependencies{RPC: rpc, Wallets: r.Wallets}
	if req.Broadcast {
		if r.Signer == nil {
			return nil, fmt.Errorf("sweep service cannot sign evm calls")
		}
		claimDir, err := ResolveClaimDir(req.ClaimDir)
		if err != nil {
			return nil, err
		}
		deps.Signer, deps.Claimer = r.Signer, FileClaimer{Dir: claimDir}
	}
	return NewService(deps)
}

// ResolveClaimDir picks the claim directory from the flag, then the environment.
func ResolveClaimDir(flagValue string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	if dir := strings.TrimSpace(os.Getenv(ClaimDirEnv)); dir != "" {
		return dir, nil
	}
	if state := strings.TrimSpace(os.Getenv(StateDirEnv)); state != "" {
		return filepath.Join(state, locksSubdir), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve claim directory: %w", err)
	}
	return filepath.Join(home, defaultStateDir, locksSubdir), nil
}

func runPassphrase(ctx context.Context, req RunRequest) (string, error) {
	if req.PassphraseStdin {
		return ReadPassphraseLine(os.Stdin)
	}
	vault, err := DefaultVaultPassphrase()
	if err != nil {
		return "", err
	}
	return vault.Read(ctx, req.Request.WalletID)
}

func mustJSON(value interface{}) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%+v", value)
	}
	return string(encoded)
}
