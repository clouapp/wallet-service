package blindpay_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp/blindpay"
)

// A real 32-byte Solana public key: the Serum DEX v3 program id.
const walletAddr = "9xQeWvG816bUx9EPjHmaT23yvVM2ZWbrrpZb9PusVFin"

const walletPath = "/v1/instances/in_000000000001/customers/re_000000000001/blockchain-wallets"

func TestEnsureBlockchainWallet_FindsWithoutCreating(t *testing.T) {
	client, calls := feeServer(t, `[
		{"id":"bw_other0000000","network":"solana","address":"`+walletAddr+`"},
		{"id":"bw_match0000000","network":"solana_devnet","address":"`+walletAddr+`"}]`, `{}`)
	id, err := blindpay.EnsureBlockchainWallet(blindpay.New(nil, nil, nil), context.Background(), client, "re_000000000001", "solana_devnet", walletAddr)
	require.NoError(t, err)
	assert.Equal(t, "bw_match0000000", id)
	got := calls()
	require.Len(t, got, 1)
	assert.Equal(t, http.MethodGet, got[0].method)
	assert.Equal(t, walletPath, got[0].path)
}

func TestEnsureBlockchainWallet_CreatesWhenAbsentOrOtherNetwork(t *testing.T) {
	client, calls := feeServer(t, `[{"id":"bw_other0000000","network":"solana","address":"`+walletAddr+`"}]`,
		`{"id":"bw_new000000000"}`)
	a := blindpay.New(nil, nil, nil)
	fixedClock(a, "2026-10-22T14:10:00Z")
	id, err := blindpay.EnsureBlockchainWallet(a, context.Background(), client, "re_000000000001", "solana_devnet", walletAddr)
	require.NoError(t, err)
	assert.Equal(t, "bw_new000000000", id)
	got := calls()
	require.Len(t, got, 2)
	post := got[1]
	assert.Equal(t, http.MethodPost, post.method)
	assert.Equal(t, walletPath, post.path)
	assert.Equal(t, "bw:re_000000000001:solana_devnet:"+walletAddr+":2026102214", post.idempotency)
	var body map[string]any
	require.NoError(t, json.Unmarshal(post.body, &body))
	assert.Equal(t, map[string]any{
		"name": "xip-9xQeWvG8", "network": "solana_devnet", "address": walletAddr,
		"is_account_abstraction": true, "is_self_custody": true,
	}, body)
}

// A wallet deleted on the dashboard is evicted on WALLETS_NOT_FOUND; the
// create in a later hour carries a new key, so it is not answered with the
// deleted id for the rest of BlindPay's 24h replay window.
func TestEnsureBlockchainWallet_KeyMovesWithTheHour(t *testing.T) {
	client, calls := feeServer(t, `[]`, `{"id":"bw_new000000000"}`)
	a := blindpay.New(nil, nil, nil)
	fixedClock(a, "2026-10-22T14:59:59Z")
	_, err := blindpay.EnsureBlockchainWallet(a, context.Background(), client, "re_000000000001", "solana", walletAddr)
	require.NoError(t, err)
	blindpay.ForgetBlockchainWallet(a, "in_000000000001", "re_000000000001", "solana", walletAddr)
	fixedClock(a, "2026-10-22T15:00:00Z")
	_, err = blindpay.EnsureBlockchainWallet(a, context.Background(), client, "re_000000000001", "solana", walletAddr)
	require.NoError(t, err)

	var keys []string
	for _, call := range calls() {
		if call.method == http.MethodPost {
			keys = append(keys, call.idempotency)
		}
	}
	base := "bw:re_000000000001:solana:" + walletAddr
	assert.Equal(t, []string{base + ":2026102214", base + ":2026102215"}, keys)
}

// Concurrent first estimates for one wallet register it once.
func TestEnsureBlockchainWallet_ConcurrentFirstCallsMakeOneCreate(t *testing.T) {
	client, calls := slowFeeServer(t, `[]`, `{"id":"bw_new000000000"}`)
	a := blindpay.New(nil, nil, nil)

	ids, errs := concurrently(8, func() (string, error) {
		return blindpay.EnsureBlockchainWallet(a, context.Background(), client, "re_000000000001", "solana", walletAddr)
	})
	for i := range ids {
		require.NoError(t, errs[i])
		assert.Equal(t, "bw_new000000000", ids[i])
	}
	got := calls()
	require.Len(t, got, 2, "%v", got)
	assert.Equal(t, http.MethodGet, got[0].method)
	assert.Equal(t, http.MethodPost, got[1].method)
}

// The first caller giving up must not fail the callers sharing its flight.
func TestEnsureBlockchainWallet_ACancelledFirstCallerDoesNotFailTheOthers(t *testing.T) {
	client, _ := slowFeeServer(t, `[]`, `{"id":"bw_new000000000"}`)
	a := blindpay.New(nil, nil, nil)

	first, cancel := context.WithCancel(context.Background())
	firstErr := make(chan error, 1)
	go func() {
		_, err := blindpay.EnsureBlockchainWallet(a, first, client, "re_000000000001", "solana", walletAddr)
		firstErr <- err
	}()
	time.Sleep(30 * time.Millisecond)
	secondID := make(chan string, 1)
	secondErr := make(chan error, 1)
	go func() {
		id, err := blindpay.EnsureBlockchainWallet(a, context.Background(), client, "re_000000000001", "solana", walletAddr)
		secondID <- id
		secondErr <- err
	}()
	time.Sleep(30 * time.Millisecond)
	cancel()

	require.ErrorIs(t, <-firstErr, context.Canceled)
	require.NoError(t, <-secondErr)
	assert.Equal(t, "bw_new000000000", <-secondID)
}

func TestEnsureBlockchainWallet_CachesPerProcess(t *testing.T) {
	client, calls := feeServer(t, `[{"id":"bw_match0000000","network":"solana","address":"`+walletAddr+`"}]`, `{}`)
	a := blindpay.New(nil, nil, nil)
	for range 2 {
		id, err := blindpay.EnsureBlockchainWallet(a, context.Background(), client, "re_000000000001", "solana", walletAddr)
		require.NoError(t, err)
		assert.Equal(t, "bw_match0000000", id)
	}
	assert.Len(t, calls(), 1)
}

func TestEnsureBlockchainWallet_RefusesBadInputWithoutCalling(t *testing.T) {
	client, calls := feeServer(t, `[]`, `{}`)
	a := blindpay.New(nil, nil, nil)
	for _, bad := range []string{"", "0x1234567890abcdef1234567890abcdef12345678", "1111111111111111111111111111111"} {
		_, err := blindpay.EnsureBlockchainWallet(a, context.Background(), client, "re_000000000001", "solana", bad)
		assert.ErrorIs(t, err, ramp.ErrInvalidWallet, bad)
	}
	_, err := blindpay.EnsureBlockchainWallet(a, context.Background(), client, "", "solana", walletAddr)
	assert.ErrorIs(t, err, ramp.ErrIntegrationRequired)
	assert.Empty(t, calls())
}

func TestValidSolanaAddress(t *testing.T) {
	assert.True(t, blindpay.ValidSolanaAddress(walletAddr))
	assert.True(t, blindpay.ValidSolanaAddress("11111111111111111111111111111111")) // 32 zero bytes
	assert.False(t, blindpay.ValidSolanaAddress("1111111111111111111111111111111")) // 31 bytes
	assert.False(t, blindpay.ValidSolanaAddress(walletAddr+"1"))                    // 33 bytes
	assert.False(t, blindpay.ValidSolanaAddress("0OIl"+walletAddr[4:]))             // chars outside the alphabet
	assert.False(t, blindpay.ValidSolanaAddress(""))
}
