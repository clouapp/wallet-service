package providers

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/pkg/egress"
)

// TestChain_ProviderWriteChecks_RefuseWhatTheAdaptersOrTheRPCGuardWouldRefuse: a
// fallback URL the Bitcoin adapter would drop, an XRP stream URL that is not wss://,
// and any of them whose host is a metadata address are refused before they are
// sealed, with the rule as the message and never the URL.
func TestChain_ProviderWriteChecks_RefuseWhatTheAdaptersOrTheRPCGuardWouldRefuse(t *testing.T) {
	checks := chainProviderWriteChecks(egress.NewGuard(egress.RPCPolicy(false), nil))
	ctx := context.Background()
	utxo := checks[settings.UTXOFallbackGroup(models.ChainLTC)]
	stream := checks[settings.GroupXRPStream]
	if utxo == nil || stream == nil {
		t.Fatal("the utxo fallback and xrp stream groups must have a write check")
	}

	for _, accepted := range []string{
		"none",
		"https://litecoin-mainnet.gateway.tatum.io",
		"electrum+ssl://electrum-ltc.bysh.me:50002?cert_sha256=fdf3c121181c14100d8740007ed546de388099c7090315ca2cf995bb4620c41d, https://1.1.1.1/api",
		"electrum+tcp://127.0.0.1:50001",
	} {
		if err := utxo(ctx, map[string]string{settings.KeyFallbackRPCURLs: accepted}); err != nil {
			t.Fatalf("%q refused: %v", accepted, err)
		}
	}
	for _, refused := range []string{
		"ftp://example.com",
		"electrum+tcp://1.1.1.1:50001",
		"electrum+ssl://no-port.example",
		"https://169.254.169.254/latest",
		"electrum+ssl://169.254.169.254:50002",
		" , ",
	} {
		err := utxo(ctx, map[string]string{settings.KeyFallbackRPCURLs: refused})
		assertFieldRefusal(t, err, settings.KeyFallbackRPCURLs, refused)
	}
	if err := utxo(ctx, map[string]string{"api_key": "only-the-key"}); err != nil {
		t.Fatalf("a write without the list is not checked: %v", err)
	}

	if err := stream(ctx, map[string]string{settings.KeyStreamURL: "wss://s.altnet.rippletest.net:51233"}); err != nil {
		t.Fatalf("a wss endpoint was refused: %v", err)
	}
	for _, refused := range []string{"ws://1.1.1.1:6006", "https://s.altnet.rippletest.net", "wss://169.254.169.254:443"} {
		assertFieldRefusal(t, stream(ctx, map[string]string{settings.KeyStreamURL: refused}), settings.KeyStreamURL, refused)
	}
}

func assertFieldRefusal(t *testing.T, err error, key, value string) {
	t.Helper()
	var validation *settings.ValidationError
	if !errors.As(err, &validation) || len(validation.Fields[key]) == 0 {
		t.Fatalf("%q: want a refusal on %s, got %v", value, key, err)
	}
	if trimmed := strings.Trim(value, " ,"); trimmed != "" && strings.Contains(validation.Fields[key][0], trimmed) {
		t.Fatalf("%q: the refusal carries the URL: %q", value, validation.Fields[key][0])
	}
}

type storedChainProviders struct {
	tronKey  string
	fallback settings.UTXOFallback
	err      error
}

func (s storedChainProviders) TronAPIKey(_ context.Context, envKey string) string {
	if s.tronKey == "" {
		return envKey
	}
	return s.tronKey
}

func (s storedChainProviders) EffectiveUTXOFallback(context.Context, string) (settings.UTXOFallback, error) {
	return s.fallback, s.err
}

func TestUTXO_FallbackConfig_StoredFieldsWinAndAFailedReadKeepsTheEnvironment(t *testing.T) {
	urls, key, data := utxoFallbackConfig(storedChainProviders{fallback: settings.UTXOFallback{
		RPCURLs: "none", UseRPCURLs: true, TatumDataAPIURL: "https://data.example", UseTatumDataAPI: true,
	}}, models.ChainLTC)
	if urls != "none" || data != "https://data.example" {
		t.Fatalf("stored fields not used: %q %q", urls, data)
	}
	envURLs, envKey, envData := utxoFallbackConfig(nil, models.ChainLTC)
	if key != envKey {
		t.Fatal("an unstored key must keep the environment value")
	}
	failedURLs, _, failedData := utxoFallbackConfig(storedChainProviders{err: errors.New("db down")}, models.ChainLTC)
	if failedURLs != envURLs || failedData != envData {
		t.Fatal("a failed read must keep the environment values")
	}
}
