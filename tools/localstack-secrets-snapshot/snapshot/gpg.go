package snapshot

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
)

const (
	gpgBinary         = "gpg"
	gpgHomePrefix     = "gnupg-"
	minSeedKeyBytes   = 32
	gpgStdoutToStdout = "-"
)

// The exact gpg invocation of the Python tool: AES256, iterated+salted S2K with
// SHA512 and the maximum count, passphrase from the seed key file, throwaway home.
var (
	gpgBaseArgs    = []string{"--batch", "--yes", "--quiet", "--no-tty", "--no-symkey-cache", "--pinentry-mode", "loopback"}
	gpgEncryptArgs = []string{"--symmetric", "--cipher-algo", "AES256", "--s2k-mode", "3", "--s2k-digest-algo", "SHA512", "--s2k-count", "65011712"}
)

// Crypter encrypts and decrypts snapshot payloads.
type Crypter interface {
	Encrypt(ctx context.Context, plaintext []byte) ([]byte, error)
	Decrypt(ctx context.Context, path string) ([]byte, error)
}

// GPGCrypter shells out to gpg with the seed key file as passphrase file.
type GPGCrypter struct {
	KeyFile string
}

// RequireSeedKey refuses to run without a seed key of the minimum size.
func RequireSeedKey(keyFile string) error {
	info, err := os.Stat(keyFile)
	if err != nil || !info.Mode().IsRegular() {
		return safeErrorf("seed key %s is missing; snapshots disabled", keyFile)
	}
	if info.Size() < minSeedKeyBytes {
		return safeErrorf("seed key %s is too short", keyFile)
	}
	return nil
}

func (g GPGCrypter) Encrypt(ctx context.Context, plaintext []byte) ([]byte, error) {
	args := append(append([]string{}, gpgEncryptArgs...), "--output", gpgStdoutToStdout)
	return g.run(ctx, args, plaintext)
}

func (g GPGCrypter) Decrypt(ctx context.Context, path string) ([]byte, error) {
	return g.run(ctx, []string{"--decrypt", path}, nil)
}

func (g GPGCrypter) run(ctx context.Context, args []string, stdin []byte) ([]byte, error) {
	home, err := os.MkdirTemp("", gpgHomePrefix)
	if err != nil {
		return nil, safeErrorf("gpg home: %v", err)
	}
	defer os.RemoveAll(home)

	full := append(append([]string{}, gpgBaseArgs...), "--homedir", home, "--passphrase-file", g.KeyFile)
	command := exec.CommandContext(ctx, gpgBinary, append(full, args...)...)
	if stdin != nil {
		command.Stdin = bytes.NewReader(stdin)
	}
	var stdout bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			return nil, safeErrorf("gpg exited with %d", exitError.ExitCode())
		}
		return nil, safeErrorf("gpg could not run: %v", err)
	}
	return stdout.Bytes(), nil
}

func lookPathGPG() (string, error) {
	return exec.LookPath(gpgBinary)
}

// SafeError is a failure whose message never contains secret material.
type SafeError struct {
	message string
}

func (e *SafeError) Error() string {
	return e.message
}

func safeErrorf(format string, args ...any) error {
	return &SafeError{message: fmt.Sprintf(format, args...)}
}
