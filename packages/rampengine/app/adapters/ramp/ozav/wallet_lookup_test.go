package ozav

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

// A deposit.credited names the wallet, not the customer: this is how the
// webhook finds whose deposit it is.
func TestCustomerIDForWalletReadsTheWalletsOwner(t *testing.T) {
	adapter, fake := newFakeAdapter(t, map[string]fakeReply{
		"GET /wallets/ozav_wal_1": {body: `{"id":"ozav_wal_1","customer_id":"ozav_cus_1"}`},
	})

	customerID, err := adapter.CustomerIDForWallet(context.Background(), "ozav_wal_1")

	require.NoError(t, err)
	require.Equal(t, "ozav_cus_1", customerID)
	require.NotNil(t, fake.call(http.MethodGet, "/wallets/ozav_wal_1"))
}

func TestCustomerIDForWalletRefusesAnUnknownWallet(t *testing.T) {
	adapter, _ := newFakeAdapter(t, map[string]fakeReply{
		"GET /wallets/ozav_wal_gone": {status: http.StatusNotFound, body: `{"error":{"code":"NOT_FOUND"}}`},
	})

	_, err := adapter.CustomerIDForWallet(context.Background(), "ozav_wal_gone")

	require.ErrorIs(t, err, ramp.ErrProviderFailed)
	var upstream *UpstreamError
	require.ErrorAs(t, err, &upstream)
	require.Equal(t, "NOT_FOUND", upstream.Code)
}

func TestCustomerIDForWalletReportsAnUpstreamFailure(t *testing.T) {
	adapter, _ := newFakeAdapter(t, map[string]fakeReply{
		"GET /wallets/ozav_wal_1": {status: http.StatusServiceUnavailable, body: `{"error":{"code":"API_V2_DISABLED"}}`},
	})

	_, err := adapter.CustomerIDForWallet(context.Background(), "ozav_wal_1")

	require.ErrorIs(t, err, ramp.ErrRetryableUpstream)
}

func TestCustomerIDForWalletRefusesAWalletWithoutAnOwner(t *testing.T) {
	adapter, _ := newFakeAdapter(t, map[string]fakeReply{
		"GET /wallets/ozav_wal_1": {body: `{"id":"ozav_wal_1"}`},
	})

	_, err := adapter.CustomerIDForWallet(context.Background(), "ozav_wal_1")

	require.ErrorContains(t, err, "has no customer_id")
}

// No wallet id, no call.
func TestCustomerIDForWalletNeedsAWalletID(t *testing.T) {
	adapter, fake := newFakeAdapter(t, map[string]fakeReply{})

	_, err := adapter.CustomerIDForWallet(context.Background(), "")

	require.Error(t, err)
	require.Empty(t, fake.requests)
}
