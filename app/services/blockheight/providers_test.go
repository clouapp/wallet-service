package blockheight

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/activity"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/pkg/httpclient"
)

const (
	heightSettingsKey = "bh-settings-key-9f3a"
	heightEnvKey      = "bh-env-key-4c21"
	heightCiphertext  = "enc:v1:" + heightSettingsKey
)

func TestNewProviders_WithoutAKeyFunctionLeavesEVMOnItsRPC(t *testing.T) {
	providers := NewProviders(nil, map[string]string{})

	_, hasEVM := providers[models.AdapterTypeEVM]
	assert.False(t, hasEVM)
	assert.NotNil(t, providers[models.AdapterTypeBitcoin])
	assert.NotNil(t, providers[models.AdapterTypeSolana])
}

func TestNewProviders_AsksForTheKeyAtUseTimeAndUsesIt(t *testing.T) {
	var calls int
	keys := []string{" " + heightSettingsKey + " ", heightEnvKey}
	providers := NewProviders(func(context.Context) string {
		calls++
		return keys[0]
	}, map[string]string{models.ChainETH: models.NetworkEthereumSepolia})
	if calls != 0 {
		t.Fatal("the etherscan key was read when the providers were built")
	}

	seen := make([]string, 0, 2)
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		seen = append(seen, r.URL.Query().Get("apikey"))
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x10"}`))
	}))
	t.Cleanup(srv.Close)
	pointEtherscanAt(t, providers, srv)

	logs := captureLogs(t)
	height, err := providers[models.AdapterTypeEVM].GetBlockHeight(context.Background(), models.ChainETH)
	require.NoError(t, err)
	assert.Equal(t, uint64(0x10), height)
	if calls != 1 {
		t.Fatal("the provider did not ask for the etherscan key")
	}
	requireSameKey(t, seen, heightSettingsKey)

	keys[0] = heightEnvKey
	_, err = providers[models.AdapterTypeEVM].GetBlockHeight(context.Background(), models.ChainETH)
	require.NoError(t, err)
	if calls != 2 || hits != 2 {
		t.Fatal("a later height check reused the key captured on the first call")
	}
	requireSameKey(t, seen[1:], heightEnvKey)
	requireLogsOmit(t, logs.String(), heightSettingsKey, heightEnvKey, heightCiphertext)
}

func TestNewProviders_BlankKeySkipsEtherscan(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
	}))
	t.Cleanup(srv.Close)

	providers := NewProviders(func(context.Context) string { return " \t " }, map[string]string{})
	pointEtherscanAt(t, providers, srv)

	_, err := providers[models.AdapterTypeEVM].GetBlockHeight(context.Background(), models.ChainETH)
	require.ErrorIs(t, err, ErrTipFromChainRPC)
	if hits != 0 {
		t.Fatal("a blank etherscan key called the provider")
	}
}

func TestNewProviders_EnabledGroupUsesTheOpenedKey(t *testing.T) {
	logs := captureLogs(t)
	seen := ""
	providers, srv := heightProviders(t, etherscanHeightStore{
		rows: map[string]string{
			"enabled": "true",
			"api_key": heightCiphertext,
		},
	}, &seen)
	t.Cleanup(srv.Close)

	_, err := providers[models.AdapterTypeEVM].GetBlockHeight(context.Background(), models.ChainETH)
	require.NoError(t, err)
	requireSameKey(t, []string{seen}, heightSettingsKey)
	requireLogsOmit(t, logs.String(), heightSettingsKey, heightEnvKey, heightCiphertext)
}

func TestNewProviders_MissingGroupUsesTheEnvKey(t *testing.T) {
	logs := captureLogs(t)
	seen := ""
	providers, srv := heightProviders(t, etherscanHeightStore{}, &seen)
	t.Cleanup(srv.Close)

	_, err := providers[models.AdapterTypeEVM].GetBlockHeight(context.Background(), models.ChainETH)
	require.NoError(t, err)
	requireSameKey(t, []string{seen}, heightEnvKey)
	requireLogsOmit(t, logs.String(), heightSettingsKey, heightEnvKey, heightCiphertext)
}

func TestNewProviders_DisabledUnsealedAndFailedReadUseTheEnvKey(t *testing.T) {
	logs := captureLogs(t)

	cases := []etherscanHeightStore{
		{rows: map[string]string{"enabled": "false", "api_key": heightCiphertext}},
		{rows: map[string]string{"enabled": "true", "api_key": heightSettingsKey}},
		{err: errors.New(heightSettingsKey + " " + heightCiphertext)},
	}
	for _, store := range cases {
		seen := ""
		providers, srv := heightProviders(t, store, &seen)
		_, err := providers[models.AdapterTypeEVM].GetBlockHeight(context.Background(), models.ChainETH)
		srv.Close()
		require.NoError(t, err)
		requireSameKey(t, []string{seen}, heightEnvKey)
	}
	requireLogsOmit(t, logs.String(), heightSettingsKey, heightEnvKey, heightCiphertext)
}

func TestNewProviders_BitcoinUsesTheTestnet4AwareProvider(t *testing.T) {
	providers := NewProviders(nil, map[string]string{models.ChainBTC: models.NetworkBitcoinTestnet4})

	routed, ok := providers[models.AdapterTypeBitcoin].(*NetworkRouted)
	require.True(t, ok)
	_, ok = routed.inner.(*BitcoinProvider)
	assert.True(t, ok)
	assert.Equal(t, TipSourceBitcoinTestnet4, providerChainIDByNetwork[models.NetworkBitcoinTestnet4])
}

func heightProviders(t *testing.T, store etherscanHeightStore, seen *string) (map[string]Provider, *httptest.Server) {
	t.Helper()
	service := settings.NewService(settings.Deps{Store: store, Sealer: heightPrefixSealer{}, Cache: nil, Activity: heightDiscardActivity{}})
	providers := NewProviders(func(ctx context.Context) string {
		return service.EtherscanKeyForHeight(ctx, heightEnvKey)
	}, map[string]string{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = r.URL.Query().Get("apikey")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x11"}`))
	}))
	pointEtherscanAt(t, providers, srv)
	return providers, srv
}

func pointEtherscanAt(t *testing.T, providers map[string]Provider, srv *httptest.Server) {
	t.Helper()
	routed, ok := providers[models.AdapterTypeEVM].(*NetworkRouted)
	require.True(t, ok)
	etherscan, ok := routed.inner.(*EtherscanProvider)
	require.True(t, ok)
	etherscan.client = httpclient.Wrap(srv.Client())
	etherscan.baseURL = srv.URL
}

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &buf
}

func requireSameKey(t *testing.T, seen []string, want string) {
	t.Helper()
	if len(seen) != 1 || seen[0] != want {
		t.Fatal("block height provider did not use the expected etherscan key")
	}
}

func requireLogsOmit(t *testing.T, logs string, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if secret != "" && strings.Contains(logs, secret) {
			t.Fatal("a log line included an etherscan key")
		}
	}
}

type etherscanHeightStore struct {
	rows map[string]string
	err  error
}

func (s etherscanHeightStore) ListGroup(context.Context, uuid.UUID, string) ([]models.Setting, error) {
	return nil, s.err
}

func (s etherscanHeightStore) UpsertMany(context.Context, uuid.UUID, string, map[string]string) error {
	if s.err != nil {
		return s.err
	}
	return nil
}

func (s etherscanHeightStore) ListPlatform(_ context.Context, group string) ([]models.Setting, error) {
	if s.err != nil {
		return nil, s.err
	}
	if group != "provider_etherscan" {
		return nil, nil
	}
	rows := make([]models.Setting, 0, len(s.rows))
	for key, value := range s.rows {
		rows = append(rows, models.Setting{Group: group, Key: key, Value: value})
	}
	return rows, nil
}

type heightPrefixSealer struct{}

func (heightPrefixSealer) Seal(plaintext string) (string, error) {
	return "enc:v1:" + plaintext, nil
}

func (heightPrefixSealer) Open(value string) (string, error) {
	raw, ok := strings.CutPrefix(value, "enc:v1:")
	if !ok {
		return "", errors.New("open refused")
	}
	return raw, nil
}

type heightDiscardActivity struct{}

func (heightDiscardActivity) Within(ctx context.Context, fn func(context.Context) error) error {
	if fn == nil {
		return errors.New("activity callback is required")
	}
	return fn(ctx)
}

func (heightDiscardActivity) Append(context.Context, models.AccountActivity) error { return nil }

var _ activity.Writer = heightDiscardActivity{}
