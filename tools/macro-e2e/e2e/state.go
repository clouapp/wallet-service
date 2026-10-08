// Package e2e holds the e2e helpers of the Wallets back end: the persistent state
// dir, the e2e API environment and process, the off-chain MPC pre-flight, and the
// guarded one-shot funding transfers (send-from-base, consolidate).
package e2e

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	StateDirEnv        = "MACRO_E2E_STATE_DIR"
	BackDirEnv         = "MACRO_WALLETS_BACK_DIR"
	defaultStateSubdir = ".local/state/macro-e2e"
	backModulePath     = "module github.com/macrowallets/waas"
	goModFile          = "go.mod"

	StateDirMode    os.FileMode = 0o700
	PrivateFileMode os.FileMode = 0o600

	apiBinaryName      = "waas-e2e-bin"
	APIHealthURL       = "http://127.0.0.1:2002/health"
	restartLockName    = "wallets-api-restart.lock"
	recordingLockName  = "custody-e2e-recording.lock"
	fundingLedgerName  = "ledger.json"
	ledgerLockName     = "funding-ledger.lock"
	apiEnvironName     = "wallets-api.environ"
	apiPIDName         = "wallets-api.pid"
	apiLogName         = "wallets-api.log"
	partialFilePrefix  = "."
	partialFileSuffix  = ".partial"
	environSeparator   = "\x00"
	environKeyValueSep = "="
)

// Paths of the persistent e2e runtime state (outside /tmp, survives reboots). The
// same directory is used by the Markets e2e (macro-markets/front/e2e/helpers/e2eState.ts,
// wallet-vault.py) and the Wallets front e2e. Only state and secrets live there.
type Paths struct {
	StateDir      string
	BackDir       string
	APIBinary     string
	APIEnviron    string
	APIPIDFile    string
	APILog        string
	LocksDir      string
	RestartLock   string
	FundingDir    string
	FundingLedger string
	LedgerLock    string
	RecordingLock string
}

// NewPaths derives every path from the state dir and the back-end checkout.
func NewPaths(stateDir, backDir string) Paths {
	locks := filepath.Join(stateDir, "locks")
	funding := filepath.Join(stateDir, "funding")
	return Paths{
		StateDir:      stateDir,
		BackDir:       backDir,
		APIBinary:     filepath.Join(stateDir, "bin", apiBinaryName),
		APIEnviron:    filepath.Join(stateDir, apiEnvironName),
		APIPIDFile:    filepath.Join(stateDir, "run", apiPIDName),
		APILog:        filepath.Join(stateDir, "logs", apiLogName),
		LocksDir:      locks,
		RestartLock:   filepath.Join(locks, restartLockName),
		FundingDir:    funding,
		FundingLedger: filepath.Join(funding, fundingLedgerName),
		LedgerLock:    filepath.Join(locks, ledgerLockName),
		RecordingLock: filepath.Join(stateDir, recordingLockName),
	}
}

// DefaultPaths uses MACRO_E2E_STATE_DIR (default ~/.local/state/macro-e2e) and the
// macro-wallets/back checkout found from MACRO_WALLETS_BACK_DIR or the working dir.
func DefaultPaths() (Paths, error) {
	stateDir := strings.TrimSpace(os.Getenv(StateDirEnv))
	if stateDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Paths{}, fmt.Errorf("home directory: %w", err)
		}
		stateDir = filepath.Join(home, defaultStateSubdir)
	}
	backDir, err := findBackDir()
	if err != nil {
		return Paths{}, err
	}
	return NewPaths(expandHome(stateDir), backDir), nil
}

func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
	}
	return path
}

func findBackDir() (string, error) {
	if configured := strings.TrimSpace(os.Getenv(BackDirEnv)); configured != "" {
		if !isBackDir(configured) {
			return "", fmt.Errorf("%s=%s is not the macro-wallets back end (no %s with %q)", BackDirEnv, configured, goModFile, backModulePath)
		}
		return filepath.Abs(configured)
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("working directory: %w", err)
	}
	for {
		if isBackDir(dir) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("run from macro-wallets/back (or set %s)", BackDirEnv)
		}
		dir = parent
	}
}

func isBackDir(dir string) bool {
	content, err := os.ReadFile(filepath.Join(dir, goModFile))
	return err == nil && bytes.HasPrefix(content, []byte(backModulePath+"\n"))
}

// EnsurePrivateDir creates path (and parents) and forces mode 0700 on it.
func EnsurePrivateDir(path string) error {
	if err := os.MkdirAll(path, StateDirMode); err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	if err := os.Chmod(path, StateDirMode); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	return nil
}

// WritePrivateFile writes data atomically to path with mode 0600, through
// .<name>.partial in the same directory.
func WritePrivateFile(path string, data []byte) error {
	if err := EnsurePrivateDir(filepath.Dir(path)); err != nil {
		return err
	}
	partial := filepath.Join(filepath.Dir(path), partialFilePrefix+filepath.Base(path)+partialFileSuffix)
	handle, err := os.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, PrivateFileMode)
	if err != nil {
		return fmt.Errorf("open %s: %w", partial, err)
	}
	_, writeErr := handle.Write(data)
	closeErr := handle.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		_ = os.Remove(partial)
		return fmt.Errorf("write %s: %w", partial, err)
	}
	if err := os.Chmod(partial, PrivateFileMode); err != nil {
		return fmt.Errorf("chmod %s: %w", partial, err)
	}
	if err := os.Rename(partial, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

// ReadAPIEnviron parses the NUL-separated KEY=VALUE environment the e2e API runs with.
func ReadAPIEnviron(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s is missing; run `macro-e2e capture-env` first", path)
	}
	environment := map[string]string{}
	for _, entry := range strings.Split(string(raw), environSeparator) {
		key, value, found := strings.Cut(entry, environKeyValueSep)
		if !found {
			continue
		}
		environment[key] = value
	}
	return environment, nil
}

// EncodeEnviron renders environment sorted by key as "KEY=VALUE\0" entries.
func EncodeEnviron(environment map[string]string) []byte {
	var buffer bytes.Buffer
	for _, key := range sortedKeys(environment) {
		buffer.WriteString(key + environKeyValueSep + environment[key] + environSeparator)
	}
	return buffer.Bytes()
}

// EnvironList renders environment for os/exec (sorted, KEY=VALUE).
func EnvironList(environment map[string]string) []string {
	list := make([]string, 0, len(environment))
	for _, key := range sortedKeys(environment) {
		list = append(list, key+environKeyValueSep+environment[key])
	}
	return list
}
