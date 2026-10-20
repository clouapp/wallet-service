package ingest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/chainregistry"
	"github.com/macrowallets/waas/tests/mocks"
)

// recordedPushes is a PushRecorder in memory; err fails every record.
type recordedPushes struct {
	mu     sync.Mutex
	chains []string
	at     []time.Time
	err    error
}

func (r *recordedPushes) RecordPush(_ context.Context, chainID string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.chains = append(r.chains, chainID)
	r.at = append(r.at, at)
	return nil
}

func pushService(pushes PushRecorder) *Service {
	registry := chain.NewRegistry()
	registry.RegisterChain(mocks.NewMockChain("eth"))
	svc := NewService(Deps{Registry: registry})
	svc.SetPushRecorder(pushes)
	return svc
}

func TestIngest_Records_WhenAWebhookOfAKnownChainArrived(t *testing.T) {
	pushes := &recordedPushes{}
	svc := pushService(pushes)

	before := time.Now().UTC()
	require.NoError(t, svc.Ingest(t.Context(), InboundEvent{ChainID: "eth"}))
	after := time.Now().UTC()

	require.Equal(t, []string{"eth"}, pushes.chains)
	assert.False(t, pushes.at[0].Before(before) || pushes.at[0].After(after), "push recorded at %s, outside [%s, %s]", pushes.at[0], before, after)
	assert.Equal(t, time.UTC, pushes.at[0].Location())

	err := svc.Ingest(t.Context(), InboundEvent{ChainID: "unknown_chain"})
	assert.ErrorIs(t, err, chainregistry.ErrUnknownChain)
	assert.Equal(t, []string{"eth"}, pushes.chains, "a webhook for a chain the registry does not hold is not a push")
}

func TestIngest_APushThatCannotBeRecordedNeverFailsTheWebhook(t *testing.T) {
	svc := pushService(&recordedPushes{err: errors.New("dial tcp 10.0.0.3:6379: connect: connection refused")})
	assert.NoError(t, svc.Ingest(t.Context(), InboundEvent{ChainID: "eth"}))
}

func TestIngest_WithoutARecorderRecordsNothing(t *testing.T) {
	svc := pushService(nil)
	assert.NoError(t, svc.Ingest(t.Context(), InboundEvent{ChainID: "eth"}))
}
