package e2evault

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

const testPassphrase = "correct horse battery staple"

func writeVaultFixture(t *testing.T, keyMode os.FileMode, walletID uuid.UUID) VaultPassphrase {
	t.Helper()
	dir := t.TempDir()
	key := filepath.Join(dir, "vault.key")
	if err := os.WriteFile(key, []byte(strings.Repeat("k", vaultKeyMinBytes)), keyMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(key, keyMode); err != nil {
		t.Fatal(err)
	}
	wallets := filepath.Join(dir, vaultWalletsDir)
	if err := os.MkdirAll(wallets, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wallets, walletID.String()+vaultRecordSuffix), []byte("encrypted"), 0o600); err != nil {
		t.Fatal(err)
	}
	return VaultPassphrase{VaultDir: dir, KeyFile: key}
}

func TestVault_Passphrase_ReturnsOnlyAVerifiedPassphrase(t *testing.T) {
	walletID := uuid.New()
	vault := writeVaultFixture(t, 0o600, walletID)
	vault.decrypt = func(context.Context, string, string) ([]byte, error) {
		return []byte(`{"passphrase":"` + testPassphrase + `","passphrase_verified":true}`), nil
	}
	if got, err := vault.Read(context.Background(), walletID); err != nil || got != testPassphrase {
		t.Fatalf("got %q err %v", got, err)
	}

	vault.decrypt = func(context.Context, string, string) ([]byte, error) {
		return []byte(`{"passphrase":"` + testPassphrase + `","passphrase_verified":false}`), nil
	}
	_, err := vault.Read(context.Background(), walletID)
	if err == nil || strings.Contains(err.Error(), testPassphrase) {
		t.Fatalf("unverified passphrase: %v", err)
	}
	if _, err := vault.Read(context.Background(), uuid.New()); err == nil {
		t.Fatal("wallet without a record: expected an error")
	}
	if _, err := vault.Read(context.Background(), uuid.Nil); err == nil {
		t.Fatal("nil wallet id: expected an error")
	}
}

func TestVault_Passphrase_RefusesAKeyOthersCanRead(t *testing.T) {
	walletID := uuid.New()
	vault := writeVaultFixture(t, 0o644, walletID)
	vault.decrypt = func(context.Context, string, string) ([]byte, error) {
		t.Fatal("decrypted with a world-readable key")
		return nil, nil
	}
	if _, err := vault.Read(context.Background(), walletID); err == nil {
		t.Fatal("expected an error")
	}
}

func TestDefault_Vault_PassphraseHonoursEnvironment(t *testing.T) {
	t.Setenv(vaultDirEnv, "/tmp/vault-dir")
	t.Setenv(vaultKeyEnv, "/tmp/vault.key")
	vault, err := DefaultVaultPassphrase()
	if err != nil || vault.VaultDir != "/tmp/vault-dir" || vault.KeyFile != "/tmp/vault.key" {
		t.Fatalf("vault = %+v, %v", vault, err)
	}
}
