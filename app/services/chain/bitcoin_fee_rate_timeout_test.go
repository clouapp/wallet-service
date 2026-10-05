package chain

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/macrowallets/waas/app/models"
)

const (
	slowFeeTestTimeout   = 50 * time.Millisecond
	slowFeeTestStall     = 2 * time.Second
	slowFeeTestMaxReturn = time.Second
)

// slowRecommendedFees serves litecoinspace's /v1/fees/recommended, stalling (like the
// real endpoint does for 15-20 s at times) while slow is set.
type slowRecommendedFees struct {
	srv  *httptest.Server
	slow atomic.Bool
	hits atomic.Int32
}

func newSlowRecommendedFees(t *testing.T, body string) *slowRecommendedFees {
	t.Helper()
	fees := &slowRecommendedFees{}
	fees.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != ltcEsploraPrefix+mempoolRecommendedFeesPath {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		fees.hits.Add(1)
		if fees.slow.Load() {
			select {
			case <-time.After(slowFeeTestStall):
			case <-r.Context().Done():
				return
			}
		}
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(fees.srv.Close)
	return fees
}

func (f *slowRecommendedFees) adapter() *BitcoinLive {
	live := NewBitcoinLive(BitcoinConfig{
		ChainIDStr: models.ChainLTC, ChainName: "Litecoin", NativeSymbol: models.NativeLTC,
		RPCURL: f.srv.URL + ltcEsploraPrefix, IsTestnet: true, Confirmations: 6,
	})
	live.feeRateTimeout = slowFeeTestTimeout
	return live
}

func timedFeePolicy(t *testing.T, live *BitcoinLive) btcFeePolicy {
	t.Helper()
	started := time.Now()
	policy := live.feePolicy(context.Background())
	if elapsed := time.Since(started); elapsed > slowFeeTestMaxReturn {
		t.Fatalf("feePolicy took %s; a stalled fee source must be cut at the fee-rate timeout", elapsed)
	}
	return policy
}

func TestFeeRateFetchIsBoundedAndFallsBackToTheFlatFee(t *testing.T) {
	fees := newSlowRecommendedFees(t, `{"hourFee":2}`)
	fees.slow.Store(true)

	policy := timedFeePolicy(t, fees.adapter())

	if policy.milliSatPerVByte != 0 || policy.flatFee != flatTestFee {
		t.Fatalf("policy %+v, want the flat fee when no network rate was ever read", policy)
	}
}

func TestStalledFeeRateFetchUsesTheLastNetworkRate(t *testing.T) {
	fees := newSlowRecommendedFees(t, `{"hourFee":2}`)
	live := fees.adapter()
	if policy := live.feePolicy(context.Background()); policy.milliSatPerVByte != 2000 {
		t.Fatalf("fresh policy %+v", policy)
	}

	fees.slow.Store(true)
	live.feeRates.fetchedAt = time.Now().Add(-btcFeeRateCacheTTL)
	policy := timedFeePolicy(t, live)

	if policy.milliSatPerVByte != 2000 {
		t.Fatalf("policy %+v, want the last network rate while it is younger than %s", policy, btcFeeRateStaleTTL)
	}
	if hits := fees.hits.Load(); hits != 2 {
		t.Fatalf("recommended fees fetched %d times, want a refresh attempt once the fresh cache expired", hits)
	}
}

func TestStalledFeeRateFetchIgnoresAnOldNetworkRate(t *testing.T) {
	fees := newSlowRecommendedFees(t, `{"hourFee":2}`)
	live := fees.adapter()
	live.feePolicy(context.Background())

	fees.slow.Store(true)
	live.feeRates.fetchedAt = time.Now().Add(-btcFeeRateStaleTTL)
	policy := timedFeePolicy(t, live)

	if policy.milliSatPerVByte != 0 || policy.flatFee != flatTestFee {
		t.Fatalf("policy %+v, want the flat fee once the last rate is %s old", policy, btcFeeRateStaleTTL)
	}
}

func TestAFailedFeeRateFetchIsNotRetriedWithinTheCacheTTL(t *testing.T) {
	fees := newSlowRecommendedFees(t, `{"hourFee":2}`)
	live := fees.adapter()
	live.feePolicy(context.Background())
	fees.slow.Store(true)
	live.feeRates.fetchedAt = time.Now().Add(-btcFeeRateCacheTTL)

	for i := 0; i < 3; i++ {
		if policy := timedFeePolicy(t, live); policy.milliSatPerVByte != 2000 {
			t.Fatalf("call %d policy %+v", i, policy)
		}
	}
	if hits := fees.hits.Load(); hits != 2 {
		t.Fatalf("recommended fees fetched %d times; one stalled refresh must serve the next calls from the last rate", hits)
	}

	fees.slow.Store(false)
	live.feeRates.failedAt = time.Now().Add(-btcFeeRateCacheTTL)
	if policy := live.feePolicy(context.Background()); policy.milliSatPerVByte != 2000 || fees.hits.Load() != 3 {
		t.Fatalf("policy %+v hits %d; the fetch must be retried once the failure is btcFeeRateCacheTTL old", policy, fees.hits.Load())
	}
	if live.feeRates.recentlyFailed(time.Now()) {
		t.Fatal("a successful fetch must clear the failure")
	}
}

func TestFeeRateCacheStaleEdgeCases(t *testing.T) {
	now := time.Now()
	var missing *btcFeeRateCache
	if _, ok := missing.stale(now); ok {
		t.Fatal("a nil cache has no rate")
	}
	missing.markFailed(now)
	if missing.recentlyFailed(now) {
		t.Fatal("a nil cache records no failure")
	}
	empty := &btcFeeRateCache{}
	if _, ok := empty.stale(now); ok {
		t.Fatal("an empty cache has no rate")
	}
	cache := &btcFeeRateCache{}
	cache.put(1500, now)
	if rate, ok := cache.stale(now.Add(btcFeeRateStaleTTL - time.Second)); !ok || rate != 1500 {
		t.Fatalf("stale rate %d %t", rate, ok)
	}
	if _, ok := cache.stale(now.Add(btcFeeRateStaleTTL)); ok {
		t.Fatal("a rate exactly btcFeeRateStaleTTL old is too old")
	}
}
