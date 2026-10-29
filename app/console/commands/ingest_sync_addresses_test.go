package commands

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingChainSync struct {
	chains []string
	err    error
}

func (r *recordingChainSync) SyncChainAddresses(_ context.Context, chainID string) error {
	r.chains = append(r.chains, chainID)
	return r.err
}

func TestIngest_SyncAddresses_PushesTheNamedChainAndReportsAFailure(t *testing.T) {
	cmd := NewIngestSyncAddresses(&recordingChainSync{})
	ext := cmd.Extend()
	assert.Equal(t, "ingest:sync-addresses", cmd.Signature())
	assert.Equal(t, "ingest", ext.Category)
	require.Len(t, ext.Arguments, 1)

	ok := &recordingChainSync{}
	require.NoError(t, NewIngestSyncAddresses(ok).sync.SyncChainAddresses(context.Background(), "sol"))
	assert.Equal(t, []string{"sol"}, ok.chains)

	failing := &recordingChainSync{err: errors.New("provider sync addresses: rate limited")}
	assert.Error(t, NewIngestSyncAddresses(failing).sync.SyncChainAddresses(context.Background(), "sol"))
	assert.Panics(t, func() { NewIngestSyncAddresses(nil) })
}
