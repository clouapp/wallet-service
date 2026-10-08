package commands

import (
	"errors"
	"strings"
	"testing"

	"github.com/macrowallets/waas/app/services/security"
)

func TestRedacted_Line_OmitsRPCCredential(t *testing.T) {
	const fixture = "fixture-rpc-query-key"
	security.ConfigureRedaction([]string{"btc.example"}, nil)
	t.Cleanup(func() { security.ConfigureRedaction(nil, nil) })

	err := errors.New(`get native balance: Get "https://user:` + fixture + `@btc.example/v2/` + fixture + `?apikey=` + fixture + `": dial tcp`)
	line := redactedLine("refresh failed: " + err.Error())
	if strings.Contains(line, fixture) {
		t.Fatal("console line wrote the RPC credential")
	}
	if !strings.Contains(line, "refresh failed") {
		t.Fatal("console line dropped the failure context")
	}
	if strings.Contains(redactedError(err).Error(), fixture) {
		t.Fatal("returned error wrote the RPC credential")
	}
}

func TestRedacted_Line_OmitsPassphraseAssignment(t *testing.T) {
	const fixture = "fixture-passphrase-value"
	line := redactedLine("preflight failed: passphrase=" + fixture)
	if strings.Contains(line, fixture) {
		t.Fatal("console line wrote the passphrase")
	}
}
