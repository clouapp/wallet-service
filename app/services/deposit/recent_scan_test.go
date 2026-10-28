package deposit

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/memcache"
	"github.com/macrowallets/waas/tests/mocks"
)

// recentCheckpoints models durable, monotonic checkpoint writes across process
// restarts. Faults leave the stored row unchanged, as a failed database write does.
type recentCheckpoints struct {
	blocks                       map[string]uint64
	readErr, writeErr, deleteErr map[string]error
}

func (r *recentCheckpoints) Find(_ context.Context, key string) (uint64, error) {
	if err := r.readErr[key]; err != nil {
		return 0, err
	}
	block, ok := r.blocks[key]
	if !ok {
		return 0, models.ErrRepositoryNotFound
	}
	return block, nil
}
func (r *recentCheckpoints) Advance(_ context.Context, key string, block uint64) (uint64, error) {
	if err := r.writeErr[key]; err != nil {
		return 0, err
	}
	r.blocks[key] = max(r.blocks[key], block)
	return r.blocks[key], nil
}
func (r *recentCheckpoints) Set(_ context.Context, key string, block uint64) error {
	if err := r.writeErr[key]; err != nil {
		return err
	}
	r.blocks[key] = block
	return nil
}
func (r *recentCheckpoints) Delete(_ context.Context, key string) error {
	if err := r.deleteErr[key]; err != nil {
		return err
	}
	delete(r.blocks, key)
	return nil
}

// Only the pending lookup is used by a scan of empty blocks. Unexpected transaction
// access fails the test through the unimplemented embedded port.
type recentEmptyTransactions struct {
	transactionStore
	confirmationPasses int
}

func (r *recentEmptyTransactions) FindPendingByChain(context.Context, string) ([]models.Transaction, error) {
	r.confirmationPasses++
	return nil, nil
}

type recentFixture struct {
	head         uint64
	checkpoints  *recentCheckpoints
	adapter      *mocks.MockChain
	transactions *recentEmptyTransactions
	service      *Service
	blocks       []uint64
	mu           sync.Mutex
	failAt       uint64
}

func newRecentFixture(t *testing.T, enabled bool) *recentFixture {
	t.Helper()
	f := &recentFixture{head: 1000, checkpoints: &recentCheckpoints{
		blocks:  map[string]uint64{"arbitrum": 100},
		readErr: map[string]error{}, writeErr: map[string]error{}, deleteErr: map[string]error{},
	}}
	f.adapter = mocks.NewMockChain("arbitrum")
	f.adapter.GetLatestBlockFn = func(context.Context) (uint64, error) { return f.head, nil }
	f.adapter.ScanBlockFn = func(_ context.Context, block uint64) ([]types.DetectedTransfer, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.blocks = append(f.blocks, block)
		if block == f.failAt {
			return nil, errors.New("RPC unavailable")
		}
		return nil, nil
	}
	f.restart(t, enabled)
	return f
}

func (f *recentFixture) restart(t *testing.T, enabled bool) {
	t.Helper()
	registry := chain.NewRegistry()
	registry.RegisterChain(f.adapter)
	f.service = NewService(Deps{Registry: registry, Checkpoints: f.checkpoints, Cache: memcache.New()})
	f.transactions = &recentEmptyTransactions{}
	f.service.txRepo = f.transactions
	f.service.sleep = func(context.Context, time.Duration) error { return nil }
	require.NoError(t, f.service.SetScanOptions(ScanOptions{BatchBlocks: 5, CatchUpBlocks: 20, Concurrency: 1}))
	if enabled {
		require.NoError(t, f.service.SetRecentScanPolicy([]string{"arbitrum"}, 10))
	}
	f.blocks = nil
}

func TestService_RecentScan_LeavesHistoricalCoverageContiguous(t *testing.T) {
	f := newRecentFixture(t, true)
	require.NoError(t, f.service.ScanLatestBlocks(context.Background(), "arbitrum"))
	assert.Equal(t, uint64(105), f.checkpoints.blocks["arbitrum"])
	assert.Equal(t, uint64(1000), f.checkpoints.blocks["recent:arbitrum"])
	assert.Equal(t, []uint64{991, 992, 993, 994, 995, 996, 997, 998, 999, 1000, 101, 102, 103, 104, 105}, f.blocks)
	assert.Equal(t, 1, f.transactions.confirmationPasses)
}

func TestService_RecentScan_RestartResumesInsteadOfSkippingToANewTip(t *testing.T) {
	f := newRecentFixture(t, true)
	require.NoError(t, f.service.ScanLatestBlocks(context.Background(), "arbitrum"))
	f.head = 1100
	f.restart(t, true)
	require.NoError(t, f.service.ScanLatestBlocks(context.Background(), "arbitrum"))
	assert.Equal(t, uint64(1020), f.checkpoints.blocks["recent:arbitrum"])
	assert.Equal(t, uint64(105), f.checkpoints.blocks["arbitrum"], "live catch-up takes priority over more history")
	require.Len(t, f.blocks, 20)
	assert.Equal(t, uint64(1001), f.blocks[0])
	assert.Equal(t, 1, f.transactions.confirmationPasses)
}

func TestService_RecentScan_FailedBlockIsRetriedAfterRestart(t *testing.T) {
	f := newRecentFixture(t, true)
	f.failAt = 995
	err := f.service.ScanLatestBlocks(context.Background(), "arbitrum")
	require.ErrorContains(t, err, "could not be recorded nor saved as pending")
	assert.Equal(t, uint64(994), f.checkpoints.blocks["recent:arbitrum"])
	assert.Equal(t, uint64(100), f.checkpoints.blocks["arbitrum"])
	assert.Equal(t, 1, f.transactions.confirmationPasses, "RPC failure must not starve confirmations")
	f.failAt = 0
	f.head = 1100
	f.restart(t, true)
	require.NoError(t, f.service.ScanLatestBlocks(context.Background(), "arbitrum"))
	assert.Equal(t, uint64(995), f.blocks[0])
	assert.Equal(t, uint64(1014), f.checkpoints.blocks["recent:arbitrum"])
}

func TestService_RecentScan_DatabaseFailureDoesNotReseedOrAdvance(t *testing.T) {
	for _, scenario := range []string{"read", "initialize", "advance"} {
		t.Run(scenario, func(t *testing.T) {
			f := newRecentFixture(t, true)
			key := recentCheckpointKey("arbitrum")
			switch scenario {
			case "read":
				f.checkpoints.readErr[key] = errors.New("database unavailable")
			case "initialize":
				f.checkpoints.writeErr[key] = errors.New("database unavailable")
			case "advance":
				f.checkpoints.blocks[key] = 990
				f.checkpoints.writeErr[key] = errors.New("database unavailable")
			}
			require.ErrorContains(t, f.service.ScanLatestBlocks(context.Background(), "arbitrum"), "database unavailable")
			assert.Equal(t, uint64(100), f.checkpoints.blocks["arbitrum"])
			if scenario == "advance" {
				assert.Equal(t, uint64(990), f.checkpoints.blocks[key])
				delete(f.checkpoints.writeErr, key)
				f.restart(t, true)
				require.NoError(t, f.service.ScanLatestBlocks(context.Background(), "arbitrum"))
				assert.Equal(t, uint64(991), f.blocks[0], "an uncommitted scan must replay safely")
			} else {
				assert.Empty(t, f.blocks)
				assert.NotContains(t, f.checkpoints.blocks, key)
			}
		})
	}
}

func TestService_RecentScan_IsOptInAndDoesNotActivateForASmallLag(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		f := newRecentFixture(t, enabled)
		if enabled {
			f.head = 110
		}
		require.NoError(t, f.service.ScanLatestBlocks(context.Background(), "arbitrum"))
		assert.NotContains(t, f.checkpoints.blocks, "recent:arbitrum")
		assert.Equal(t, uint64(101), f.blocks[0])
	}
}

func TestService_RecentScan_OperatorResetAndNetworkAlignmentDropTheRecentCursor(t *testing.T) {
	for _, operation := range []string{"set", "drop"} {
		t.Run(operation, func(t *testing.T) {
			f := newRecentFixture(t, false)
			f.checkpoints.blocks["recent:arbitrum"] = 999
			if operation == "set" {
				_, err := f.service.SetCheckpoint(context.Background(), "arbitrum", 50, true)
				require.NoError(t, err)
				assert.Equal(t, uint64(50), f.checkpoints.blocks["arbitrum"])
			} else {
				_, _, err := f.service.DropCheckpoint(context.Background(), "arbitrum")
				require.NoError(t, err)
				assert.NotContains(t, f.checkpoints.blocks, "arbitrum")
			}
			assert.NotContains(t, f.checkpoints.blocks, "recent:arbitrum")
		})
	}
}

func TestService_RecentScan_RefusesInvalidPolicy(t *testing.T) {
	for _, id := range []string{"", " eth", "recent:eth", strings.Repeat("a", 44)} {
		s := NewService(Deps{})
		require.Error(t, s.SetRecentScanPolicy([]string{id}, 10))
	}
	s := NewService(Deps{})
	require.Error(t, s.SetRecentScanPolicy([]string{"eth"}, 0))
	require.Error(t, s.SetRecentScanPolicy([]string{"eth"}, 10001))
}

func TestService_RecentScan_RecentCheckpointAheadOfAnotherNetworkFailsClosed(t *testing.T) {
	f := newRecentFixture(t, true)
	f.checkpoints.blocks["recent:arbitrum"] = 100000
	err := f.service.ScanLatestBlocks(context.Background(), "arbitrum")
	require.ErrorIs(t, err, ErrCheckpointAheadOfHead)
	assert.Empty(t, f.blocks)
	assert.Equal(t, uint64(100000), f.checkpoints.blocks["recent:arbitrum"])
	assert.Equal(t, uint64(100), f.checkpoints.blocks["arbitrum"])
}

func TestService_RecentScan_BackfillFailureStillConfirmsRecentDeposits(t *testing.T) {
	f := newRecentFixture(t, true)
	f.failAt = 103
	err := f.service.ScanLatestBlocks(context.Background(), "arbitrum")
	require.ErrorContains(t, err, "could not be recorded nor saved as pending")
	assert.Equal(t, uint64(102), f.checkpoints.blocks["arbitrum"])
	assert.Equal(t, uint64(1000), f.checkpoints.blocks["recent:arbitrum"])
	assert.Equal(t, 1, f.transactions.confirmationPasses)
}

func TestService_RecentScan_FailedRecentResetDoesNotMoveTheHistoricalCursor(t *testing.T) {
	f := newRecentFixture(t, true)
	f.checkpoints.blocks["recent:arbitrum"] = 999
	f.checkpoints.deleteErr["recent:arbitrum"] = errors.New("database unavailable")
	_, err := f.service.SetCheckpoint(context.Background(), "arbitrum", 50, true)
	require.ErrorContains(t, err, "database unavailable")
	assert.Equal(t, uint64(100), f.checkpoints.blocks["arbitrum"])
	assert.Equal(t, uint64(999), f.checkpoints.blocks["recent:arbitrum"])
}

func TestService_RecentScan_ReorganizationCoversRecentBlocksAfterPolicyIsDisabled(t *testing.T) {
	f := newRecentFixture(t, false)
	f.checkpoints.blocks["recent:arbitrum"] = 999
	height, found, err := f.service.reorgScanCheckpoint(context.Background(), "arbitrum")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, uint64(999), height)
	assert.Equal(t, uint64(100), f.checkpoints.blocks["arbitrum"], "reorganization coverage does not advance history")
}
