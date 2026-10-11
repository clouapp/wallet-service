package mpc

import (
	"context"
	"errors"
	"math/big"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bnb-chain/tss-lib/v2/ecdsa/keygen"
)

// fakePreParams hands out numbered pre-params (NTildei = 1, 2, 3…) and counts calls.
type fakePreParams struct {
	calls atomic.Int64
	fail  atomic.Int64 // the next fail calls return an error
}

func (f *fakePreParams) generate(ctx context.Context) (*keygen.LocalPreParams, error) {
	n := f.calls.Add(1)
	if f.fail.Load() > 0 {
		f.fail.Add(-1)
		return nil, errors.New("safe prime generation timed out")
	}
	return &keygen.LocalPreParams{NTildei: big.NewInt(n)}, nil
}

func runPool(t *testing.T, pool *PreParamsPool) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		pool.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Run did not return after its context was cancelled")
		}
	})
	waitFor(t, "the pool to run", pool.running.Load)
	return cancel
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestPreParamsPool_Take_ReturnsNilWhenThePoolIsNotRunning(t *testing.T) {
	pool := NewPreParamsPool(2, (&fakePreParams{}).generate)

	got, err := pool.Take(context.Background())

	if err != nil || got != nil {
		t.Fatalf("Take on a pool that is not running = (%v, %v), want (nil, nil) so keygen generates inline", got, err)
	}
}

func TestPreParamsPool_Take_ReturnsNilOnANilPool(t *testing.T) {
	var pool *PreParamsPool

	got, err := pool.Take(context.Background())

	if err != nil || got != nil {
		t.Fatalf("Take on a nil pool = (%v, %v), want (nil, nil)", got, err)
	}
}

func TestPreParamsPool_Run_FillsUpToItsSizeAndStops(t *testing.T) {
	gen := &fakePreParams{}
	pool := NewPreParamsPool(3, gen.generate)
	runPool(t, pool)

	waitFor(t, "3 generations", func() bool { return gen.calls.Load() == 3 })
	time.Sleep(50 * time.Millisecond)

	if got := gen.calls.Load(); got != 3 {
		t.Fatalf("generations with nothing taken = %d, want 3", got)
	}
}

func TestPreParamsPool_Take_RefillsTheSlotItFreed(t *testing.T) {
	gen := &fakePreParams{}
	pool := NewPreParamsPool(2, gen.generate)
	runPool(t, pool)
	waitFor(t, "a full pool", func() bool { return gen.calls.Load() == 2 })

	if _, err := pool.Take(context.Background()); err != nil {
		t.Fatalf("Take: %v", err)
	}

	waitFor(t, "the refill", func() bool { return gen.calls.Load() == 3 })
}

func TestPreParamsPool_Take_HandsOutEachPreParamsOnce(t *testing.T) {
	pool := NewPreParamsPool(4, (&fakePreParams{}).generate)
	runPool(t, pool)

	seen := map[int64]bool{}
	for i := range 6 {
		got, err := pool.Take(context.Background())
		if err != nil || got == nil {
			t.Fatalf("Take #%d = (%v, %v)", i+1, got, err)
		}
		n := got.NTildei.Int64()
		if seen[n] {
			t.Fatalf("pre-params %d handed out twice", n)
		}
		seen[n] = true
	}
}

func TestPreParamsPool_Take_WaitsForTheNextGenerationWhenEmpty(t *testing.T) {
	release := make(chan struct{})
	gen := func(ctx context.Context) (*keygen.LocalPreParams, error) {
		select {
		case <-release:
			return &keygen.LocalPreParams{NTildei: big.NewInt(7)}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	pool := NewPreParamsPool(1, gen)
	runPool(t, pool)

	var wg sync.WaitGroup
	var got *keygen.LocalPreParams
	var err error
	wg.Go(func() {
		got, err = pool.Take(context.Background())
	})
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()

	if err != nil || got == nil || got.NTildei.Int64() != 7 {
		t.Fatalf("Take on an empty running pool = (%v, %v), want the next generated pre-params", got, err)
	}
}

func TestPreParamsPool_Take_StopsWaitingWhenItsContextEnds(t *testing.T) {
	block := func(ctx context.Context) (*keygen.LocalPreParams, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	pool := NewPreParamsPool(1, block)
	runPool(t, pool)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	got, err := pool.Take(ctx)

	if got != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Take past its deadline = (%v, %v), want (nil, context.DeadlineExceeded)", got, err)
	}
}

func TestPreParamsPool_Run_RetriesAfterAGenerationError(t *testing.T) {
	gen := &fakePreParams{}
	gen.fail.Store(1)
	pool := NewPreParamsPool(1, gen.generate)
	pool.retryAfter = time.Millisecond
	runPool(t, pool)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := pool.Take(ctx)

	if err != nil || got == nil || got.NTildei.Int64() != 2 {
		t.Fatalf("Take after one failed generation = (%v, %v), want the second generation", got, err)
	}
}

func TestPreParamsPool_Run_DoesNothingWhenItsSizeIsZero(t *testing.T) {
	gen := &fakePreParams{}
	pool := NewPreParamsPool(0, gen.generate)

	done := make(chan struct{})
	go func() {
		pool.Run(context.Background())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run with size 0 did not return")
	}
	if gen.calls.Load() != 0 || pool.running.Load() {
		t.Fatalf("size 0: generations = %d, running = %v; want 0 and false", gen.calls.Load(), pool.running.Load())
	}
}
