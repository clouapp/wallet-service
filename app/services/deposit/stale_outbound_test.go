package deposit

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
	"github.com/macrowallets/waas/tests/mocks"
)

func recordedAgo(t *testing.T, id uuid.UUID, age time.Duration) {
	t.Helper()
	if _, err := facades.Orm().Query().Exec(`UPDATE transactions SET created_at = ? WHERE id = ?`, time.Now().UTC().Add(-age), id); err != nil {
		t.Fatal(err)
	}
}

func TestRunConfirmationCheck_WarnsEachPassAboutOutboundTransfersLeftUnsettled(t *testing.T) {
	fixtures.TestDB(t)
	logs := captureLogs(t)
	registry := chain.NewRegistry()
	mockChain := mocks.NewMockChain("eth")
	mockChain.RequiredConfirmationsVal = 3
	mockChain.GetTransactionBlockVal = 0
	registry.RegisterChain(mockChain)
	w := fixtures.InsertWallet(t, "eth")
	stale := fixtures.InsertTransaction(t, w.ID, nil, "eth", models.TxTypeWithdrawal, "confirming", "eth", "1000", 0)
	recordedAgo(t, stale.ID, 7*time.Hour)
	fresh := fixtures.InsertTransaction(t, w.ID, nil, "eth", models.TxTypeWithdrawal, "confirming", "eth", "1000", 0)
	recordedAgo(t, fresh.ID, time.Hour)
	deposit := fixtures.InsertTransaction(t, w.ID, nil, "eth", models.TxTypeDeposit, "pending", "eth", "1000", 0)
	recordedAgo(t, deposit.ID, 7*time.Hour)

	svc := newDepositSvc(registry, newWebhookSvc())
	svc.SetStaleOutboundAfter(6 * time.Hour)
	for run := 0; run < 2; run++ {
		if err := svc.RunConfirmationCheck(context.Background()); err != nil {
			t.Fatal(err)
		}
	}

	var warned []string
	for _, record := range logs() {
		if record["msg"] == staleOutboundMessage {
			warned = append(warned, record["tx_id"].(string))
			if record["level"] != "WARN" || record["chain"] != "eth" || record["tx_hash"] != stale.TxHash {
				t.Fatalf("warning %v", record)
			}
		}
	}
	if len(warned) != 2 || warned[0] != stale.ID.String() || warned[1] != stale.ID.String() {
		t.Fatalf("warned %v, want the stale withdrawal once per pass and nothing else", warned)
	}
}

func TestStaleOutbound_Health_CountsTheUnsettledTransfersPerChain(t *testing.T) {
	fixtures.TestDB(t)
	registry := chain.NewRegistry()
	w := fixtures.InsertWallet(t, "eth")
	for _, age := range []time.Duration{7 * time.Hour, 8 * time.Hour, time.Hour} {
		tx := fixtures.InsertTransaction(t, w.ID, nil, "eth", models.TxTypeWithdrawal, "confirming", "eth", "1000", 0)
		recordedAgo(t, tx.ID, age)
	}
	settled := fixtures.InsertTransaction(t, w.ID, nil, "eth", models.TxTypeWithdrawal, "confirmed", "eth", "1000", 9)
	recordedAgo(t, settled.ID, 9*time.Hour)
	sol := fixtures.InsertWallet(t, "sol")
	seed := fixtures.InsertTransaction(t, sol.ID, nil, "sol", models.TxTypeGasSeed, "pending", "sol", "1000", 0)
	recordedAgo(t, seed.ID, 7*time.Hour)

	svc := newDepositSvc(registry, newWebhookSvc())
	svc.SetStaleOutboundAfter(6 * time.Hour)
	health := svc.StaleOutboundHealth(context.Background())
	if health.Err != nil || health.Total != 3 || health.Counts["eth"] != 2 || health.Counts["sol"] != 1 || health.After != 6*time.Hour {
		t.Fatalf("health %+v, want 2 on eth and 1 on sol", health)
	}

	fresh := fixtures.InsertTransaction(t, w.ID, nil, "eth", models.TxTypeWithdrawal, "confirming", "eth", "1000", 0)
	recordedAgo(t, fresh.ID, 10*time.Hour)
	if again := svc.StaleOutboundHealth(context.Background()); again.Total != 3 {
		t.Fatalf("a health check within a minute reads the cached counts, got %+v", again)
	}
}

// gatedCounter counts stale outbound rows as told, after gate opens when it is set.
type gatedCounter struct {
	*sweepConfirmMemory
	gate   chan struct{}
	counts map[string]int
	err    error
	calls  atomic.Int32
}

func (g *gatedCounter) CountStaleOutbound(ctx context.Context, _ time.Time) (map[string]int, error) {
	g.calls.Add(1)
	if g.gate != nil {
		select {
		case <-g.gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return g.counts, g.err
}

func TestStaleOutbound_Health_NeverWaitsOnACountInFlightAndServesTheLastOneOnError(t *testing.T) {
	counter := &gatedCounter{sweepConfirmMemory: &sweepConfirmMemory{}, counts: map[string]int{"eth": 1}}
	clock := &testClock{now: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)}
	svc := &Service{txRepo: counter, now: clock.Now}
	if got := svc.StaleOutboundHealth(context.Background()); got.Total != 1 || got.Err != nil {
		t.Fatalf("first count %+v", got)
	}

	clock.advance(staleOutboundCacheFor + time.Second)
	counter.gate, counter.counts = make(chan struct{}), map[string]int{"eth": 2}
	leader := make(chan StaleOutboundHealth, 1)
	go func() { leader <- svc.StaleOutboundHealth(context.Background()) }()
	for counter.calls.Load() < 2 {
		time.Sleep(time.Millisecond)
	}
	started := time.Now()
	short, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	follower := svc.StaleOutboundHealth(short)
	if time.Since(started) > time.Second || follower.Total != 1 {
		t.Fatalf("follower %+v after %s: a second check gets the last counts instead of waiting on the count in flight", follower, time.Since(started))
	}
	if counter.calls.Load() != 2 {
		t.Fatalf("%d counts: one at a time", counter.calls.Load())
	}
	close(counter.gate)
	if got := <-leader; got.Total != 2 {
		t.Fatalf("leader %+v", got)
	}

	clock.advance(staleOutboundCacheFor + time.Second)
	counter.gate, counter.err = nil, errors.New("dial tcp 10.0.0.2:5432: connect: connection refused")
	if got := svc.StaleOutboundHealth(context.Background()); got.Total != 2 || got.Err != nil {
		t.Fatalf("a count that fails serves the last one: %+v", got)
	}
}
