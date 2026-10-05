package e2e

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	testSolanaKeyPath = "/v2/test-alchemy-key-not-real"
	testAppKey        = "test-app-key-not-real-0123456789"
)

func newTestPaths(t *testing.T) Paths {
	t.Helper()
	root := t.TempDir()
	backDir := filepath.Join(root, "back")
	if err := os.MkdirAll(backDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return NewPaths(filepath.Join(root, "state"), backDir)
}

func requireMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != want {
		t.Fatalf("%s mode %v, want %v", path, info.Mode().Perm(), want)
	}
}

func TestWritePrivateFileUsesPrivateModesAndNoPartialRemains(t *testing.T) {
	paths := newTestPaths(t)
	target := filepath.Join(paths.StateDir, "nested", "secret.txt")
	if err := WritePrivateFile(target, []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := WritePrivateFile(target, []byte("two")); err != nil {
		t.Fatal(err)
	}
	requireMode(t, target, PrivateFileMode)
	requireMode(t, filepath.Dir(target), StateDirMode)
	if content, _ := os.ReadFile(target); string(content) != "two" {
		t.Fatalf("content %q", content)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(target), ".secret.txt.partial")); !os.IsNotExist(err) {
		t.Fatalf("partial file left behind: %v", err)
	}
}

func TestEnvironIsNULSeparatedSortedAndRoundTrips(t *testing.T) {
	environment := map[string]string{"B": "x=y=z", "A": "", "PATH": "/usr/bin:/bin"}
	encoded := EncodeEnviron(environment)
	if string(encoded) != "A=\x00B=x=y=z\x00PATH=/usr/bin:/bin\x00" {
		t.Fatalf("encoded %q", encoded)
	}
	path := filepath.Join(t.TempDir(), "environ")
	if err := os.WriteFile(path, append(encoded, []byte("garbage-without-separator\x00")...), 0o600); err != nil {
		t.Fatal(err)
	}
	decoded, err := ReadAPIEnviron(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 3 || decoded["B"] != "x=y=z" || decoded["A"] != "" {
		t.Fatalf("decoded %v", decoded)
	}
	if _, err := ReadAPIEnviron(filepath.Join(t.TempDir(), "missing")); err == nil || !strings.Contains(err.Error(), "capture-env") {
		t.Fatalf("missing environ: %v", err)
	}
}

func writeEnvDev(t *testing.T, backDir, content string) string {
	t.Helper()
	path := filepath.Join(backDir, sourceEnvFileName)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func validEnvDev() string {
	return strings.Join([]string{
		"# comment=ignored",
		"APP_KEY=" + testAppKey,
		`export PORT="2002"`,
		"DB_PORT='5432'",
		"  REDIS_PORT = 6379  ",
		"DB_DATABASE=vault",
		"AWS_ENDPOINT_URL=http://localhost:4566",
		"ETH_RPC_URL=https://example.invalid/eth",
		"SOLANA_RPC_URL=https://solana-devnet.g.alchemy.com" + testSolanaKeyPath,
		"DATABASE_URL=postgres://vault:vault@localhost:5432/vault?sslmode=disable",
		"E2E_FUNDER_PRIVATE_KEY=must-not-reach-the-api",
		"not a line",
	}, "\r\n") + "\n"
}

func TestBuildAPIEnvironmentAppliesTheE2EOverrides(t *testing.T) {
	paths := newTestPaths(t)
	source := writeEnvDev(t, paths.BackDir, validEnvDev())
	process := map[string]string{"HOME": "/home/tester", "PATH": "/bin", "USER": "", "SECRET": "not passed"}
	environment, err := BuildAPIEnvironment(source, func(key string) string { return process[key] })
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"HOME":                      "/home/tester",
		"PATH":                      "/bin",
		"PORT":                      "2002",
		"DB_PORT":                   "5432",
		"REDIS_PORT":                "6379",
		"DB_DATABASE":               E2EDatabase,
		"CHAIN_NETWORK_PROFILE":     "testnet",
		"LOCAL_DEPOSIT_SCAN_CHAINS": "sol,eth,btc,polygon,base,arbitrum,bsc,tron,ltc",
		"BTC_RPC_URL":               "https://mempool.space/testnet4/api",
		"TRON_RPC_URL":              "https://nile.trongrid.io",
		"TLTC_RPC_URL":              "https://litecoinspace.org/testnet/api",
		"ETH_RPC_URL":               "https://eth-sepolia.g.alchemy.com" + testSolanaKeyPath,
		"TETH_RPC_URL":              "https://eth-sepolia.g.alchemy.com" + testSolanaKeyPath,
		"DATABASE_URL":              "postgres://vault:vault@localhost:5432/vault_test?sslmode=disable",
	}
	for key, value := range want {
		if environment[key] != value {
			t.Errorf("%s = %q, want %q", key, environment[key], value)
		}
	}
	for _, absent := range []string{"USER", "SECRET", "E2E_FUNDER_PRIVATE_KEY"} {
		if _, present := environment[absent]; present {
			t.Errorf("%s must not reach the API environment", absent)
		}
	}
}

func TestBuildAPIEnvironmentRefusesUnusableSources(t *testing.T) {
	cases := map[string]string{
		"not an Alchemy devnet URL":  strings.Replace(validEnvDev(), "solana-devnet.g.alchemy.com", "api.devnet.solana.com", 1),
		"DATABASE_URL in .env.dev":   strings.Replace(validEnvDev(), "postgres://vault:vault@localhost:5432/vault?sslmode=disable", "mysql://x/y", 1),
		"APP_KEY is empty":           strings.Replace(validEnvDev(), "APP_KEY="+testAppKey, "APP_KEY=", 1),
		"AWS_ENDPOINT_URL is empty":  strings.Replace(validEnvDev(), "AWS_ENDPOINT_URL=http://localhost:4566", "", 1),
		"not an Alchemy devnet URL ": strings.Replace(validEnvDev(), testSolanaKeyPath, "/v3/key", 1),
	}
	for wantMessage, content := range cases {
		paths := newTestPaths(t)
		source := writeEnvDev(t, paths.BackDir, content)
		_, err := BuildAPIEnvironment(source, func(string) string { return "" })
		if err == nil || !strings.Contains(err.Error(), strings.TrimSpace(wantMessage)) {
			t.Errorf("%s: got %v", wantMessage, err)
		}
	}
	if _, err := BuildAPIEnvironment(filepath.Join(t.TempDir(), sourceEnvFileName), os.Getenv); err == nil {
		t.Error("missing .env.dev accepted")
	}
}

func TestCaptureEnvWritesPrivatelyKeepsABackupAndPrintsNoValues(t *testing.T) {
	paths := newTestPaths(t)
	writeEnvDev(t, paths.BackDir, validEnvDev())
	now := time.Date(2026, 10, 3, 5, 6, 7, 0, time.UTC)

	var dryRun bytes.Buffer
	if err := CaptureEnv(paths, true, now, &dryRun); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(paths.APIEnviron); !os.IsNotExist(err) {
		t.Fatal("dry run wrote the environ")
	}
	if !strings.Contains(dryRun.String(), "no current environ at "+paths.APIEnviron) {
		t.Errorf("dry run without a current environ: %s", dryRun.String())
	}
	if err := WritePrivateFile(paths.APIEnviron, []byte("OLD=1\x00")); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := CaptureEnv(paths, false, now, &output); err != nil {
		t.Fatal(err)
	}
	requireMode(t, paths.APIEnviron, PrivateFileMode)
	backup := paths.APIEnviron + ".prev-20261003T050607Z"
	requireMode(t, backup, PrivateFileMode)
	if content, _ := os.ReadFile(backup); string(content) != "OLD=1\x00" {
		t.Fatalf("backup %q", content)
	}
	environment, err := ReadAPIEnviron(paths.APIEnviron)
	if err != nil || environment["DB_DATABASE"] != E2EDatabase {
		t.Fatalf("environ %v %v", environment["DB_DATABASE"], err)
	}
	printed := dryRun.String() + output.String()
	for _, secret := range []string{testAppKey, "test-alchemy-key-not-real", "must-not-reach-the-api"} {
		if strings.Contains(printed, secret) {
			t.Fatalf("output leaks a secret value: %s", printed)
		}
	}
	wantLines := []string{
		"overrides: DB_DATABASE=vault_test CHAIN_NETWORK_PROFILE=testnet LOCAL_DEPOSIT_SCAN_CHAINS=sol,eth,btc,polygon,base,arbitrum,bsc,tron,ltc BTC_RPC_URL=https://mempool.space/testnet4/api TRON_RPC_URL=https://nile.trongrid.io TTRON_RPC_URL=https://nile.trongrid.io LTC_RPC_URL=https://litecoinspace.org/testnet/api TLTC_RPC_URL=https://litecoinspace.org/testnet/api" +
			" LTC_FALLBACK_RPC_URL=" + litecoinTestnetFallbacks + " TLTC_FALLBACK_RPC_URL=" + litecoinTestnetFallbacks,
		"ETH_RPC_URL TETH_RPC_URL: Alchemy Sepolia, key of SOLANA_RPC_URL (not printed)",
		"previous environ kept as " + backup,
		"wrote " + paths.APIEnviron,
	}
	for _, line := range wantLines {
		if !strings.Contains(output.String(), line+"\n") {
			t.Errorf("missing line %q in\n%s", line, output.String())
		}
	}
}

func TestCaptureEnvDryRunComparesWithTheCurrentEnvironByKeyOnly(t *testing.T) {
	paths := newTestPaths(t)
	writeEnvDev(t, paths.BackDir, validEnvDev())
	now := time.Date(2026, 10, 3, 5, 6, 7, 0, time.UTC)
	if err := CaptureEnv(paths, false, now, io.Discard); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(paths.APIEnviron)
	if err != nil {
		t.Fatal(err)
	}

	var equivalent bytes.Buffer
	if err := CaptureEnv(paths, true, now, &equivalent); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(equivalent.String(), "equivalent to "+paths.APIEnviron) {
		t.Fatalf("expected equivalence, got\n%s", equivalent.String())
	}

	current, err := ReadAPIEnviron(paths.APIEnviron)
	if err != nil {
		t.Fatal(err)
	}
	current["MANUAL_ONLY"] = "hand-edited-secret-value"
	current["APP_KEY"] = "edited-app-key-secret"
	delete(current, "PORT")
	if err := WritePrivateFile(paths.APIEnviron, EncodeEnviron(current)); err != nil {
		t.Fatal(err)
	}
	var differs bytes.Buffer
	if err := CaptureEnv(paths, true, now, &differs); err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"only in current: MANUAL_ONLY", "only generated: PORT", "different values: APP_KEY"} {
		if !strings.Contains(differs.String(), line+"\n") {
			t.Errorf("missing line %q in\n%s", line, differs.String())
		}
	}
	for _, secret := range []string{"hand-edited-secret-value", "edited-app-key-secret", testAppKey} {
		if strings.Contains(differs.String(), secret) {
			t.Fatalf("dry run leaks a value: %s", differs.String())
		}
	}
	if after, _ := os.ReadFile(paths.APIEnviron); bytes.Equal(after, original) {
		t.Fatal("test setup did not change the environ")
	}
}
