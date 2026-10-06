package etherscan

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
	"github.com/macrowallets/waas/app/services/blockheight"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/pkg/httpclient"
)

const (
	heightSettingsKey = "bh-settings-key-9f3a"
	heightEnvKey      = "bh-env-key-4c21"
	heightCiphertext  = "enc:v1:" + heightSettingsKey
)

func TestProvider_Reads_TheTip(t *testing.T) {
	for _, tc := range []struct {
		chainID    string
		chainParam string
	}{
		{models.ChainETH, "1"},
		{models.ChainPolygon, "137"},
		{models.ChainTETH, "11155111"},
		{models.ChainTPolygon, "80002"},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodGet, r.Method)
			assert.True(t, strings.HasPrefix(r.URL.Path, "/v2/api"))
			q := r.URL.Query()
			assert.Equal(t, tc.chainParam, q.Get("chainid"))
			assert.Equal(t, "proxy", q.Get("module"))
			assert.Equal(t, "eth_blockNumber", q.Get("action"))
			assert.Empty(t, q.Get("apikey"))
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x1234ab"}`))
		}))
		t.Cleanup(srv.Close)

		p := New(blockheight.EtherscanDeps{})
		p.client = httpclient.Wrap(srv.Client())
		p.baseURL = srv.URL

		height, err := p.GetBlockHeight(context.Background(), tc.chainID)
		require.NoError(t, err)
		assert.Equal(t, uint64(0x1234ab), height)
	}
}

func TestProvider_Defaults_ToEtherscan(t *testing.T) {
	p := New(blockheight.EtherscanDeps{})
	assert.Equal(t, etherscanDefaultBase, p.baseURL)
}

func TestProvider_Error_Response(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"execution reverted"}}`))
	}))
	t.Cleanup(srv.Close)

	p := New(blockheight.EtherscanDeps{})
	p.client = httpclient.Wrap(srv.Client())
	p.baseURL = srv.URL

	_, err := p.GetBlockHeight(context.Background(), models.ChainETH)
	require.Error(t, err)
	assert.ErrorIs(t, err, chain.ErrProvider)
	assert.NotContains(t, err.Error(), "execution reverted")
	assert.Contains(t, chain.CauseText(err), "execution reverted")
}

func TestProvider_Rejects_AnUnknownChain(t *testing.T) {
	p := New(blockheight.EtherscanDeps{})
	_, err := p.GetBlockHeight(context.Background(), "unknown")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown chain_id")
}

func TestProvider_Asks_ForTheKeyAtUseTimeAndUsesIt(t *testing.T) {
	var calls int
	keys := []string{" " + heightSettingsKey + " ", heightEnvKey}
	p := New(blockheight.EtherscanDeps{KeyAtUse: func(context.Context) string {
		calls++
		return keys[0]
	}})
	if calls != 0 {
		t.Fatal("the etherscan key was read when the provider was built")
	}

	seen := make([]string, 0, 2)
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		seen = append(seen, r.URL.Query().Get("apikey"))
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x10"}`))
	}))
	t.Cleanup(srv.Close)
	p.client = httpclient.Wrap(srv.Client())
	p.baseURL = srv.URL

	logs := captureLogs(t)
	height, err := p.GetBlockHeight(context.Background(), models.ChainETH)
	require.NoError(t, err)
	assert.Equal(t, uint64(0x10), height)
	if calls != 1 {
		t.Fatal("the provider did not ask for the etherscan key")
	}
	requireSameKey(t, seen, heightSettingsKey)

	keys[0] = heightEnvKey
	_, err = p.GetBlockHeight(context.Background(), models.ChainETH)
	require.NoError(t, err)
	if calls != 2 || hits != 2 {
		t.Fatal("a later height check reused the key captured on the first call")
	}
	requireSameKey(t, seen[1:], heightEnvKey)
	requireLogsOmit(t, logs.String(), heightSettingsKey, heightEnvKey, heightCiphertext)
}

func TestProvider_Blank_KeySkipsTheRequest(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
	}))
	t.Cleanup(srv.Close)

	p := New(blockheight.EtherscanDeps{KeyAtUse: func(context.Context) string { return " \t " }})
	p.client = httpclient.Wrap(srv.Client())
	p.baseURL = srv.URL

	_, err := p.GetBlockHeight(context.Background(), models.ChainETH)
	require.ErrorIs(t, err, blockheight.ErrTipFromChainRPC)
	if hits != 0 {
		t.Fatal("a blank etherscan key called the provider")
	}
}

func TestProvider_Uses_TheStaticKeyWhenThereIsNoKeyFunction(t *testing.T) {
	seen := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Query().Get("apikey")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x11"}`))
	}))
	t.Cleanup(srv.Close)

	p := New(blockheight.EtherscanDeps{APIKey: " " + heightEnvKey + " "})
	p.client = httpclient.Wrap(srv.Client())
	p.baseURL = srv.URL

	_, err := p.GetBlockHeight(context.Background(), models.ChainETH)
	require.NoError(t, err)
	requireSameKey(t, []string{seen}, heightEnvKey)
}

func TestProvider_Enabled_GroupUsesTheOpenedKey(t *testing.T) {
	logs := captureLogs(t)
	seen := ""
	p, srv := heightProvider(t, etherscanHeightStore{
		rows: map[string]string{
			"enabled": "true",
			"api_key": heightCiphertext,
		},
	}, &seen)
	t.Cleanup(srv.Close)

	_, err := p.GetBlockHeight(context.Background(), models.ChainETH)
	require.NoError(t, err)
	requireSameKey(t, []string{seen}, heightSettingsKey)
	requireLogsOmit(t, logs.String(), heightSettingsKey, heightEnvKey, heightCiphertext)
}

func TestProvider_Missing_GroupUsesTheEnvKey(t *testing.T) {
	logs := captureLogs(t)
	seen := ""
	p, srv := heightProvider(t, etherscanHeightStore{}, &seen)
	t.Cleanup(srv.Close)

	_, err := p.GetBlockHeight(context.Background(), models.ChainETH)
	require.NoError(t, err)
	requireSameKey(t, []string{seen}, heightEnvKey)
	requireLogsOmit(t, logs.String(), heightSettingsKey, heightEnvKey, heightCiphertext)
}

func TestProvider_Disabled_UnsealedAndFailedReadUseTheEnvKey(t *testing.T) {
	logs := captureLogs(t)

	cases := []etherscanHeightStore{
		{rows: map[string]string{"enabled": "false", "api_key": heightCiphertext}},
		{rows: map[string]string{"enabled": "true", "api_key": heightSettingsKey}},
		{err: errors.New(heightSettingsKey + " " + heightCiphertext)},
	}
	for _, store := range cases {
		seen := ""
		p, srv := heightProvider(t, store, &seen)
		_, err := p.GetBlockHeight(context.Background(), models.ChainETH)
		srv.Close()
		require.NoError(t, err)
		requireSameKey(t, []string{seen}, heightEnvKey)
	}
	requireLogsOmit(t, logs.String(), heightSettingsKey, heightEnvKey, heightCiphertext)
}

func heightProvider(t *testing.T, store etherscanHeightStore, seen *string) (*Provider, *httptest.Server) {
	t.Helper()
	service := settings.NewService(settings.Deps{Store: store, Sealer: heightPrefixSealer{}, Cache: nil, Activity: heightDiscardActivity{}})
	p := New(blockheight.EtherscanDeps{KeyAtUse: func(ctx context.Context) string {
		return service.EtherscanKeyForHeight(ctx, heightEnvKey)
	}})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = r.URL.Query().Get("apikey")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x11"}`))
	}))
	p.client = httpclient.Wrap(srv.Client())
	p.baseURL = srv.URL
	return p, srv
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

func TestProvider_Nil_ContextDoesNotCallHTTP(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	t.Cleanup(srv.Close)

	p := New(blockheight.EtherscanDeps{KeyAtUse: func(context.Context) string {
		t.Fatal("a nil context read the key")
		return ""
	}})
	p.client = httpclient.Wrap(srv.Client())
	p.baseURL = srv.URL

	_, err := p.GetBlockHeight(nil, models.ChainETH)
	require.Error(t, err)
	assert.False(t, called)
}
