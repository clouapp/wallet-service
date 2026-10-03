// Package e2evault reads wallet passphrases from the encrypted e2e wallet vault
// written by wallet-vault.py (macro-markets/back/tests/e2e): one symmetric gpg
// record per wallet, <vault dir>/wallets/<wallet id>.json.gpg, encrypted with the
// vault key file. Only passphrases the vault verified against share A are returned.
package e2evault

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

const (
	vaultDirEnv        = "MACRO_E2E_VAULT_DIR"
	vaultKeyEnv        = "MACRO_E2E_VAULT_KEY"
	defaultVaultDir    = ".macro-e2e-wallet-vault"
	defaultVaultKey    = ".config/macro-e2e/vault.key"
	vaultWalletsDir    = "wallets"
	vaultRecordSuffix  = ".json.gpg"
	vaultKeyMinBytes   = 32
	vaultKeyPrivateBit = 0o077
	gpgBinary          = "gpg"
)

// gpgDecryptArgs match wallet-vault.py, which writes the records.
var gpgDecryptArgs = []string{"--batch", "--yes", "--quiet", "--no-tty", "--no-symkey-cache", "--pinentry-mode", "loopback"}

// VaultPassphrase locates the vault and its key file.
type VaultPassphrase struct {
	VaultDir string
	KeyFile  string
	decrypt  func(ctx context.Context, keyFile, path string) ([]byte, error)
}

// DefaultVaultPassphrase uses MACRO_E2E_VAULT_DIR / MACRO_E2E_VAULT_KEY or their defaults.
func DefaultVaultPassphrase() (VaultPassphrase, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return VaultPassphrase{}, fmt.Errorf("home directory: %w", err)
	}
	vault := VaultPassphrase{VaultDir: filepath.Join(home, defaultVaultDir), KeyFile: filepath.Join(home, defaultVaultKey)}
	if dir := strings.TrimSpace(os.Getenv(vaultDirEnv)); dir != "" {
		vault.VaultDir = dir
	}
	if key := strings.TrimSpace(os.Getenv(vaultKeyEnv)); key != "" {
		vault.KeyFile = key
	}
	return vault, nil
}

// Read returns the verified passphrase of walletID. The caller must not log it.
func (v VaultPassphrase) Read(ctx context.Context, walletID uuid.UUID) (string, error) {
	if walletID == uuid.Nil {
		return "", fmt.Errorf("wallet id is required")
	}
	if err := requirePrivateKeyFile(v.KeyFile); err != nil {
		return "", err
	}
	record := filepath.Join(v.VaultDir, vaultWalletsDir, walletID.String()+vaultRecordSuffix)
	if _, err := os.Stat(record); err != nil {
		return "", fmt.Errorf("no vault record for wallet %s", walletID)
	}
	decrypt := v.decrypt
	if decrypt == nil {
		decrypt = gpgDecrypt
	}
	plaintext, err := decrypt(ctx, v.KeyFile, record)
	if err != nil {
		return "", err
	}
	defer zeroBytes(plaintext)
	var parsed struct {
		Passphrase         string `json:"passphrase"`
		PassphraseVerified bool   `json:"passphrase_verified"`
	}
	if err := json.Unmarshal(plaintext, &parsed); err != nil {
		return "", fmt.Errorf("vault record for wallet %s is not JSON", walletID)
	}
	if parsed.Passphrase == "" || !parsed.PassphraseVerified {
		return "", fmt.Errorf("vault has no verified passphrase for wallet %s; run wallet-vault.py backup", walletID)
	}
	return parsed.Passphrase, nil
}

func requirePrivateKeyFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("vault key %s is missing", path)
	}
	if info.Mode().Perm()&vaultKeyPrivateBit != 0 {
		return fmt.Errorf("vault key %s is readable by others; chmod 600 it", path)
	}
	if info.Size() < vaultKeyMinBytes {
		return fmt.Errorf("vault key %s is too short", path)
	}
	return nil
}

func gpgDecrypt(ctx context.Context, keyFile, path string) ([]byte, error) {
	args := append(append([]string{}, gpgDecryptArgs...), "--passphrase-file", keyFile, "--decrypt", path)
	cmd := exec.CommandContext(ctx, gpgBinary, args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("gpg could not decrypt %s (wrong key or corrupted file)", filepath.Base(path))
	}
	return stdout.Bytes(), nil
}

func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
