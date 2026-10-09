package deposit

import (
	"context"
	"errors"
	"testing"

	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/deposit/pending"
	"github.com/macrowallets/waas/tests/mocks"
)

type listedPending struct {
	entries map[string][]pending.Entry
	failing map[string]error
}

func (l listedPending) Put(context.Context, pending.Entry) error     { return nil }
func (l listedPending) Delete(context.Context, string, uint64) error { return nil }
func (l listedPending) List(_ context.Context, chainID string) ([]pending.Entry, error) {
	if err := l.failing[chainID]; err != nil {
		return nil, err
	}
	return l.entries[chainID], nil
}

func healthService(store pending.Store) *Service {
	registry := chain.NewRegistry()
	registry.RegisterChain(mocks.NewMockChain("eth"))
	registry.RegisterChain(mocks.NewMockChain("btc"))
	return &Service{registry: registry, pending: store}
}

func TestPendingHealth_Counts_BlocksPerChainAndTheirTotal(t *testing.T) {
	store := listedPending{entries: map[string][]pending.Entry{
		"eth": {{Block: 1}, {Block: 2}},
	}}

	health := healthService(store).PendingHealth(context.Background())
	if health.Err != nil || health.Total != 2 || health.Counts["eth"] != 2 || health.Counts["btc"] != 0 {
		t.Fatalf("health = %+v", health)
	}
}

func TestPendingHealth_Reports_AnUnreadableChainWithoutHidingTheOthers(t *testing.T) {
	down := errors.New("redis down")
	store := listedPending{
		entries: map[string][]pending.Entry{"btc": {{Block: 7}}},
		failing: map[string]error{"eth": down},
	}

	health := healthService(store).PendingHealth(context.Background())
	if !errors.Is(health.Err, down) || health.Total != 1 || health.Counts["btc"] != 1 {
		t.Fatalf("health = %+v", health)
	}
}

func TestPendingHealth_Reports_AMissingPendingStore(t *testing.T) {
	health := healthService(nil).PendingHealth(context.Background())
	if health.Err == nil || health.Total != 0 || health.Counts != nil {
		t.Fatalf("health = %+v", health)
	}
}
