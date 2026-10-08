package evmcall

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/macrowallets/waas/pkg/e2evault"
)

const maxPassphraseBytes = 1 << 10

// VaultPassphrase reads a verified wallet passphrase from the encrypted e2e wallet vault.
type VaultPassphrase = e2evault.VaultPassphrase

// DefaultVaultPassphrase uses MACRO_E2E_VAULT_DIR / MACRO_E2E_VAULT_KEY or their defaults.
func DefaultVaultPassphrase() (VaultPassphrase, error) {
	return e2evault.DefaultVaultPassphrase()
}

// ReadPassphraseLine reads the first line of input (e.g. stdin), never echoing it.
func ReadPassphraseLine(input io.Reader) (string, error) {
	line, err := bufio.NewReader(io.LimitReader(input, maxPassphraseBytes)).ReadString('\n')
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("read passphrase: %w", err)
	}
	passphrase := strings.TrimRight(line, "\r\n")
	if len(passphrase) < minPassphraseSize {
		return "", fmt.Errorf("passphrase on stdin must be at least %d characters", minPassphraseSize)
	}
	return passphrase, nil
}
