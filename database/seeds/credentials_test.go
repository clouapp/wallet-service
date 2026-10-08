package seeds

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
)

func TestPrint_Credentials_OmitsSeedPassphrase(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	stdout := captureStdout(t, PrintCredentials)
	written := stdout + logs.String()
	if strings.Contains(written, SeedPassphrase) {
		t.Fatal("PrintCredentials wrote the seed passphrase")
	}
	if !strings.Contains(logs.String(), "seeded wallets share one MPC passphrase") {
		t.Fatal("expected an info log that wallets share an MPC passphrase")
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	previous := os.Stdout
	os.Stdout = write
	defer func() { os.Stdout = previous }()

	fn()
	if err := write.Close(); err != nil {
		t.Fatalf("close stdout pipe: %v", err)
	}
	out, err := io.ReadAll(read)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	return string(out)
}
