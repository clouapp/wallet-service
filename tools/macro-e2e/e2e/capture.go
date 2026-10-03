package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	sourceEnvFileName       = ".env.dev"
	E2EDatabase             = "vault_test"
	alchemySolanaDevnetHost = "solana-devnet.g.alchemy.com"
	alchemySepoliaHost      = "eth-sepolia.g.alchemy.com"
	alchemyKeyPathPrefix    = "/v2/"
	environBackupStamp      = "20060102T150405Z"
	environBackupInfix      = ".prev-"
	quoteCharacters         = `'"`
	minQuotedValueLength    = 2
	printedDigestLength     = 16
)

// e2eOverride is applied in order on top of .env.dev.
type e2eOverride struct{ Key, Value string }

var (
	e2eOverrides = []e2eOverride{
		{"DB_DATABASE", E2EDatabase},
		{"CHAIN_NETWORK_PROFILE", "testnet"},
		{"LOCAL_DEPOSIT_SCAN_CHAINS", "sol,eth,btc,polygon,base,arbitrum,bsc"},
		{"BTC_RPC_URL", "https://mempool.space/testnet4/api"},
	}
	refusedDatabases   = map[string]bool{"vault": true, "vault_unit_test": true}
	processPassthrough = []string{"HOME", "PATH", "USER", "LANG", "TZ"}
	notForTheAPI       = []string{"E2E_FUNDER_PRIVATE_KEY"}
	sepoliaRPCKeys     = []string{"ETH_RPC_URL", "TETH_RPC_URL"}
	requiredAPIKeys    = []string{"APP_KEY", "PORT", "DB_PORT", "REDIS_PORT", "AWS_ENDPOINT_URL", "ETH_RPC_URL", "SOLANA_RPC_URL"}

	envLinePattern     = regexp.MustCompile(`^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$`)
	databaseURLPattern = regexp.MustCompile(`^(postgres(?:ql)?://[^/]+/)([^?]+)(.*)$`)
	pythonLineBreaks   = strings.NewReplacer("\r\n", "\n", "\r", "\n", "\v", "\n", "\f", "\n", "\x1c", "\n", "\x1d", "\n", "\x1e", "\n", "\u0085", "\n", "\u2028", "\n", "\u2029", "\n")
)

// ParseEnvFile reads KEY=VALUE lines (optionally `export`-prefixed, quotes stripped).
func ParseEnvFile(path string) (map[string]string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s is missing", path)
	}
	values := map[string]string{}
	for _, line := range strings.Split(pythonLineBreaks.Replace(string(content)), "\n") {
		match := envLinePattern.FindStringSubmatch(line)
		if match == nil || strings.HasPrefix(strings.TrimLeft(line, " \t"), "#") {
			continue
		}
		values[match[1]] = unquote(strings.TrimSpace(match[2]))
	}
	return values, nil
}

func unquote(raw string) string {
	if len(raw) >= minQuotedValueLength && raw[0] == raw[len(raw)-1] && strings.ContainsRune(quoteCharacters, rune(raw[0])) {
		return raw[1 : len(raw)-1]
	}
	return raw
}

func withE2EDatabase(databaseURL string) (string, error) {
	match := databaseURLPattern.FindStringSubmatch(databaseURL)
	if match == nil {
		return "", fmt.Errorf("DATABASE_URL in .env.dev is not a postgres URL with a database path")
	}
	return match[1] + E2EDatabase + match[3], nil
}

// alchemySepoliaURL is the Alchemy Sepolia endpoint with the key of the Alchemy Solana devnet endpoint.
func alchemySepoliaURL(solanaRPCURL string) (string, error) {
	parsed, err := url.Parse(solanaRPCURL)
	if err != nil || strings.ToLower(parsed.Hostname()) != alchemySolanaDevnetHost || !strings.HasPrefix(parsed.EscapedPath(), alchemyKeyPathPrefix) {
		return "", fmt.Errorf("SOLANA_RPC_URL is not an Alchemy devnet URL; cannot derive the Sepolia RPC")
	}
	return "https://" + alchemySepoliaHost + parsed.EscapedPath(), nil
}

// BuildAPIEnvironment merges the process passthrough, .env.dev and the e2e overrides.
func BuildAPIEnvironment(sourceEnvFile string, lookupEnv func(string) string) (map[string]string, error) {
	environment := map[string]string{}
	for _, key := range processPassthrough {
		if value := lookupEnv(key); value != "" {
			environment[key] = value
		}
	}
	fromFile, err := ParseEnvFile(sourceEnvFile)
	if err != nil {
		return nil, err
	}
	for key, value := range fromFile {
		environment[key] = value
	}
	for _, key := range notForTheAPI {
		delete(environment, key)
	}
	for _, override := range e2eOverrides {
		environment[override.Key] = override.Value
	}
	sepoliaRPCURL, err := alchemySepoliaURL(environment["SOLANA_RPC_URL"])
	if err != nil {
		return nil, err
	}
	for _, key := range sepoliaRPCKeys {
		environment[key] = sepoliaRPCURL
	}
	if databaseURL, present := environment["DATABASE_URL"]; present {
		if environment["DATABASE_URL"], err = withE2EDatabase(databaseURL); err != nil {
			return nil, err
		}
	}
	if refusedDatabases[environment["DB_DATABASE"]] {
		return nil, fmt.Errorf("refusing to run the e2e API on %s", environment["DB_DATABASE"])
	}
	for _, required := range requiredAPIKeys {
		if environment[required] == "" {
			return nil, fmt.Errorf("%s is empty after merging .env.dev and the e2e overrides", required)
		}
	}
	return environment, nil
}

// CaptureEnv builds <state dir>/wallets-api.environ (0600) for the e2e Wallets API.
// Only key names are printed, never values. The previous file is kept as
// wallets-api.environ.prev-<UTC timestamp>.
func CaptureEnv(paths Paths, dryRun bool, now time.Time, out io.Writer) error {
	environment, err := BuildAPIEnvironment(filepath.Join(paths.BackDir, sourceEnvFileName), os.Getenv)
	if err != nil {
		return err
	}
	overrides := make([]string, 0, len(e2eOverrides))
	for _, override := range e2eOverrides {
		overrides = append(overrides, override.Key+"="+override.Value)
	}
	fmt.Fprintf(out, "%d keys: %s\n", len(environment), strings.Join(sortedKeys(environment), " "))
	fmt.Fprintf(out, "overrides: %s\n", strings.Join(overrides, " "))
	fmt.Fprintf(out, "%s: Alchemy Sepolia, key of SOLANA_RPC_URL (not printed)\n", strings.Join(sepoliaRPCKeys, " "))
	if dryRun {
		return reportAgainstCurrentEnviron(paths.APIEnviron, environment, out)
	}

	if _, err := os.Stat(paths.APIEnviron); err == nil {
		backup := paths.APIEnviron + environBackupInfix + now.UTC().Format(environBackupStamp)
		if err := copyPrivatePreservingTime(paths.APIEnviron, backup); err != nil {
			return err
		}
		fmt.Fprintf(out, "previous environ kept as %s\n", backup)
	}
	if err := WritePrivateFile(paths.APIEnviron, EncodeEnviron(environment)); err != nil {
		return err
	}
	fmt.Fprintf(out, "wrote %s\n", paths.APIEnviron)
	return nil
}

// EnvironDiff names the keys two environments disagree on; values never leave it.
type EnvironDiff struct {
	OnlyCurrent, OnlyGenerated, DifferentValue []string
}

func (d EnvironDiff) Equivalent() bool {
	return len(d.OnlyCurrent) == 0 && len(d.OnlyGenerated) == 0 && len(d.DifferentValue) == 0
}

func DiffEnvirons(current, generated map[string]string) EnvironDiff {
	var diff EnvironDiff
	for _, key := range sortedKeys(current) {
		generatedValue, present := generated[key]
		switch {
		case !present:
			diff.OnlyCurrent = append(diff.OnlyCurrent, key)
		case generatedValue != current[key]:
			diff.DifferentValue = append(diff.DifferentValue, key)
		}
	}
	for _, key := range sortedKeys(generated) {
		if _, present := current[key]; !present {
			diff.OnlyGenerated = append(diff.OnlyGenerated, key)
		}
	}
	return diff
}

func environDigest(environment map[string]string) string {
	sum := sha256.Sum256(EncodeEnviron(environment))
	return hex.EncodeToString(sum[:])[:printedDigestLength]
}

func reportAgainstCurrentEnviron(currentPath string, generated map[string]string, out io.Writer) error {
	if _, err := os.Stat(currentPath); os.IsNotExist(err) {
		fmt.Fprintf(out, "no current environ at %s to compare with\n", currentPath)
		return nil
	}
	current, err := ReadAPIEnviron(currentPath)
	if err != nil {
		return err
	}
	diff := DiffEnvirons(current, generated)
	if diff.Equivalent() {
		fmt.Fprintf(out, "equivalent to %s (%d keys, sha256 %s)\n", currentPath, len(current), environDigest(current))
		return nil
	}
	fmt.Fprintf(out, "differs from %s (sha256 current %s, generated %s)\n", currentPath, environDigest(current), environDigest(generated))
	fmt.Fprintf(out, "only in current: %s\n", strings.Join(diff.OnlyCurrent, " "))
	fmt.Fprintf(out, "only generated: %s\n", strings.Join(diff.OnlyGenerated, " "))
	fmt.Fprintf(out, "different values: %s\n", strings.Join(diff.DifferentValue, " "))
	return nil
}

func copyPrivatePreservingTime(source, destination string) error {
	info, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("stat %s: %w", source, err)
	}
	content, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("read %s: %w", source, err)
	}
	if err := os.WriteFile(destination, content, PrivateFileMode); err != nil {
		return fmt.Errorf("write %s: %w", destination, err)
	}
	if err := os.Chmod(destination, PrivateFileMode); err != nil {
		return fmt.Errorf("chmod %s: %w", destination, err)
	}
	if err := os.Chtimes(destination, info.ModTime(), info.ModTime()); err != nil {
		return fmt.Errorf("preserve mtime of %s: %w", destination, err)
	}
	return nil
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
