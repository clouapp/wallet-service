package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/macrowallets/waas/tools/internal/pyjson"
)

const (
	APIURLEnv                 = "MACRO_WALLETS_API_URL"
	MarketsContainerEnv       = "MARKETS_CONTAINER"
	MarketsPHPEnv             = "MARKETS_PHP"
	WalletsDBContainerEnv     = "WALLETS_DB_CONTAINER"
	defaultAPIURL             = "http://localhost:2002"
	defaultMarketsContainer   = "markets-custody-e2e"
	defaultMarketsPHP         = "/usr/bin/php8.5.real"
	defaultWalletsDBContainer = "waas-postgres"
	walletsDBUser             = "vault"
	marketsWorkdir            = "/var/www/html"
	tokenPrefix               = "TOKEN="
	dockerBinary              = "docker"
	HTTPTimeout               = 120 * time.Second
	tokenTimeout              = 120 * time.Second
	vaultQueryTimeout         = 60 * time.Second
	queryErrorTailRunes       = 400
	rawBodyPreviewRunes       = 500
	emptyJSONObject           = "{}"
)

// Services are the external calls of the funding flows, injectable for tests.
type Services struct {
	OutboundMatches func(ctx context.Context, walletID, to, asset, baseUnits string) ([]string, error)
	SweepRows       func(ctx context.Context, query SweepQuery) ([]SweepRow, error)
	MarketsToken    func(ctx context.Context) (string, error)
	API             APIClient
}

// DockerServices reads the Markets token and vault_test through `docker exec`.
type DockerServices struct {
	MarketsContainer   string
	MarketsPHP         string
	WalletsDBContainer string
	Run                ProcessRunner
}

// DockerServicesFromEnv honours MARKETS_CONTAINER, MARKETS_PHP and WALLETS_DB_CONTAINER.
func DockerServicesFromEnv() DockerServices {
	return DockerServices{
		MarketsContainer:   envOrDefault(MarketsContainerEnv, defaultMarketsContainer),
		MarketsPHP:         envOrDefault(MarketsPHPEnv, defaultMarketsPHP),
		WalletsDBContainer: envOrDefault(WalletsDBContainerEnv, defaultWalletsDBContainer),
		Run:                ExecRunner,
	}
}

func envOrDefault(name, fallback string) string {
	if value, present := os.LookupEnv(name); present {
		return value
	}
	return fallback
}

// OutboundMatches lists vault_test outbound transactions of wallet to `to` for baseUnits of
// asset. Base units only compare within one asset (400000 wei is not 0.4 USDC), and vault_test
// stores native assets in lower case ("eth") and tokens in upper case ("USDC").
// Inputs must match UUIDPattern, AddressPattern, AssetPattern and AmountPattern (no quotes possible).
func (docker DockerServices) OutboundMatches(ctx context.Context, walletID, to, asset, baseUnits string) ([]string, error) {
	if err := requireAll([]fieldCheck{{UUIDPattern, walletID, "wallet id"}, {AddressPattern, to, "destination address"}, {AssetPattern, asset, "asset"}, {AmountPattern, baseUnits, "amount (base units)"}}); err != nil {
		return nil, err
	}
	sql := "select id || ' ' || status || ' ' || coalesce(tx_hash,'') from transactions " +
		"where wallet_id = '" + walletID + "' and direction = 'outbound' and lower(to_address) = lower('" + to + "') " +
		"and upper(asset) = '" + asset + "' and amount = '" + baseUnits + "'"
	return docker.queryVaultTest(ctx, sql)
}

// MarketsToken reads the Macro Wallets API token from the Markets crypto custody settings.
// It is returned in memory only and never printed.
func (docker DockerServices) MarketsToken(ctx context.Context) (string, error) {
	source := "echo '" + tokenPrefix + `'.app(App\Settings\CryptoCustodySettings::class)->macro_wallets_api_token.PHP_EOL;`
	command := `exec env $(tr "\0" "\n" < /proc/1/environ | grep -E "^(DB_|REDIS_|APP_)" | tr "\n" " ") ` +
		docker.MarketsPHP + ` artisan tinker --execute="$0"`
	tokenContext, cancel := context.WithTimeout(ctx, tokenTimeout)
	defer cancel()
	result, err := docker.Run(tokenContext, dockerBinary, []string{"exec", "-w", marketsWorkdir, docker.MarketsContainer, "sh", "-c", command, source}, "", os.Environ(), nil)
	defer zeroBytes(result.Stdout)
	var tokens []string
	for _, line := range splitPythonLines(string(result.Stdout)) {
		if strings.HasPrefix(line, tokenPrefix) {
			tokens = append(tokens, strings.TrimSpace(strings.TrimPrefix(line, tokenPrefix)))
		}
	}
	if err != nil || result.ExitCode != 0 || len(tokens) == 0 || tokens[len(tokens)-1] == "" {
		return "", errors.New("could not read the Markets Macro Wallets API token")
	}
	return tokens[len(tokens)-1], nil
}

// APIClient calls the external Wallets API (/api/v1) with the Markets bearer token.
type APIClient struct {
	BaseURL string
	HTTP    *http.Client
}

// APIClientFromEnv uses MACRO_WALLETS_API_URL (default http://localhost:2002).
func APIClientFromEnv() APIClient {
	return APIClient{
		BaseURL: strings.TrimRight(envOrDefault(APIURLEnv, defaultAPIURL), "/"),
		HTTP:    &http.Client{Timeout: HTTPTimeout},
	}
}

// Request sends body (nil for none) and returns the HTTP status and the decoded JSON
// response; a body that is not JSON comes back as {"raw": <first 500 characters>}.
func (client APIClient) Request(ctx context.Context, method, path, token string, body pyjson.Object) (int, any, error) {
	var payload io.Reader
	var encoded []byte
	if body != nil {
		text, err := pyjson.Dumps(body, pyjson.Default)
		if err != nil {
			return 0, nil, err
		}
		encoded = []byte(text)
		defer zeroBytes(encoded)
		payload = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, client.BaseURL+path, payload)
	if err != nil {
		return 0, nil, fmt.Errorf("build %s request: %w", method, err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	httpClient := client.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: HTTPTimeout}
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return 0, nil, fmt.Errorf("%s %s: %w", method, path, redactURLError(err))
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return response.StatusCode, nil, fmt.Errorf("read %s %s response: %w", method, path, err)
	}
	return response.StatusCode, decodeResponseBody(raw), nil
}

func decodeResponseBody(raw []byte) any {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte(emptyJSONObject)
	}
	decoded, err := pyjson.Decode(raw)
	if err != nil {
		return pyjson.Object{{Key: "raw", Value: truncateRunes(strings.ToValidUTF8(string(raw), "\uFFFD"), rawBodyPreviewRunes)}}
	}
	return decoded
}

// redactURLError drops the request URL (it may carry credentials) from transport errors.
func redactURLError(err error) error {
	var urlError *url.Error
	if errors.As(err, &urlError) && urlError.Err != nil {
		return urlError.Err
	}
	return err
}

// FindTxHash returns the first truthy tx_hash of payload, payload.data or payload.withdrawal.
func FindTxHash(payload any) string {
	root, isObject := payload.(pyjson.Object)
	if !isObject {
		return ""
	}
	containers := []any{root}
	for _, key := range []string{"data", "withdrawal"} {
		value, _ := root.Get(key)
		containers = append(containers, value)
	}
	for _, container := range containers {
		object, isObject := container.(pyjson.Object)
		if !isObject {
			continue
		}
		hash, _ := object.Get("tx_hash")
		if pythonTruthy(hash) {
			return pythonStr(hash)
		}
	}
	return ""
}

// LookupStatus is (payload.data or payload).status.
func LookupStatus(payload any) any {
	root, isObject := payload.(pyjson.Object)
	if !isObject {
		return nil
	}
	container := any(root)
	if data, _ := root.Get("data"); pythonTruthy(data) {
		container = data
	}
	object, isObject := container.(pyjson.Object)
	if !isObject {
		return nil
	}
	status, _ := object.Get("status")
	return status
}

func pythonStr(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	case bool:
		if typed {
			return "True"
		}
		return "False"
	default:
		encoded, err := pyjson.Dumps(typed, pyjson.Default)
		if err != nil {
			return fmt.Sprint(typed)
		}
		return encoded
	}
}
