package deposit

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// readFailure wraps the error a gatedReading returns; atomic.Value refuses a nil.
type readFailure struct{ err error }

// gatedReading counts its readings and, while gate is set, waits for it to close.
type gatedReading struct {
	gate   chan struct{}
	value  atomic.Int64
	err    atomic.Value
	calls  atomic.Int32
	ctxErr atomic.Value
	panics bool
}

func (g *gatedReading) read(ctx context.Context) (int64, error) {
	g.calls.Add(1)
	if g.panics {
		panic("store client crashed")
	}
	if g.gate != nil {
		<-g.gate
	}
	if err := ctx.Err(); err != nil {
		g.ctxErr.Store(err)
	}
	if failure, ok := g.err.Load().(readFailure); ok && failure.err != nil {
		return -1, failure.err
	}
	return g.value.Load(), nil
}

func (g *gatedReading) healthRead(keepFailure bool) healthRead[int64] {
	return healthRead[int64]{
		Read:        g.read,
		Unavailable: func(error) int64 { return -100 },
		KeepFailure: keepFailure,
		Failure:     "test reading",
		Busy:        errors.New("busy"),
	}
}

var snapshotNow = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

func TestHealth_Snapshot_AWaiterWakesWithTheReadingThatFinished(t *testing.T) {
	var cache healthSnapshot[int64]
	reading := &gatedReading{gate: make(chan struct{})}
	reading.value.Store(42)

	results := make(chan int64, 2)
	for i := 0; i < 2; i++ {
		go func() {
			results <- cache.get(context.Background(), snapshotNow, time.Minute, reading.healthRead(false))
		}()
	}
	for reading.calls.Load() < 1 {
		time.Sleep(time.Millisecond)
	}
	started := time.Now()
	close(reading.gate)
	for i := 0; i < 2; i++ {
		if got := <-results; got != 42 {
			t.Fatalf("check %d got %d, want the reading that finished", i, got)
		}
	}
	if waited := time.Since(started); waited > healthReadBudget/2 {
		t.Fatalf("the checks woke %s after the reading finished", waited)
	}
	if calls := reading.calls.Load(); calls != 1 {
		t.Fatalf("%d readings for two checks, want one", calls)
	}
}

func TestHealth_Snapshot_AFailedReadingIsNotRepeatedDuringTheBackoff(t *testing.T) {
	var cache healthSnapshot[int64]
	reading := &gatedReading{}
	reading.value.Store(1)
	if got := cache.get(context.Background(), snapshotNow, time.Second, reading.healthRead(false)); got != 1 {
		t.Fatalf("first reading %d", got)
	}

	reading.err.Store(readFailure{errors.New("dial tcp 10.0.0.3:6379: i/o timeout")})
	afterTTL := snapshotNow.Add(2 * time.Second)
	if got := cache.get(context.Background(), afterTTL, time.Second, reading.healthRead(false)); got != 1 {
		t.Fatalf("a failed reading serves the last one, got %d", got)
	}
	inBackoff := afterTTL.Add(healthFailureBackoff - time.Second)
	if got := cache.get(context.Background(), inBackoff, time.Second, reading.healthRead(false)); got != 1 || reading.calls.Load() != 2 {
		t.Fatalf("got %d after %d readings: no reading during the backoff", got, reading.calls.Load())
	}

	reading.err.Store(readFailure{})
	reading.value.Store(3)
	if got := cache.get(context.Background(), afterTTL.Add(healthFailureBackoff), time.Second, reading.healthRead(false)); got != 3 || reading.calls.Load() != 3 {
		t.Fatalf("got %d after %d readings: the backoff over, it reads again", got, reading.calls.Load())
	}
}

func TestHealth_Snapshot_KeepFailureServesTheFailedReadingItself(t *testing.T) {
	var cache healthSnapshot[int64]
	reading := &gatedReading{}
	reading.err.Store(readFailure{errors.New("redis down")})
	if got := cache.get(context.Background(), snapshotNow, time.Second, reading.healthRead(true)); got != -1 {
		t.Fatalf("got %d, want the failed reading, which carries its error", got)
	}
	if got := cache.get(context.Background(), snapshotNow.Add(time.Second), time.Second, reading.healthRead(true)); got != -1 || reading.calls.Load() != 1 {
		t.Fatalf("got %d after %d readings", got, reading.calls.Load())
	}
}

func TestHealth_Snapshot_AReadingThatPanicsLeavesNoReadingInFlight(t *testing.T) {
	var cache healthSnapshot[int64]
	reading := &gatedReading{panics: true}
	if got := cache.get(context.Background(), snapshotNow, time.Second, reading.healthRead(false)); got != -100 {
		t.Fatalf("got %d, want unavailable", got)
	}
	reading.panics = false
	reading.value.Store(5)
	if got := cache.get(context.Background(), snapshotNow.Add(healthFailureBackoff+time.Second), time.Second, reading.healthRead(false)); got != 5 {
		t.Fatalf("got %d: a panic must not leave a reading in flight forever", got)
	}
}

func TestHealth_Snapshot_TheReadingOutlivesACheckWhoseClientLeft(t *testing.T) {
	var cache healthSnapshot[int64]
	reading := &gatedReading{gate: make(chan struct{})}
	reading.value.Store(9)
	gone, cancel := context.WithCancel(context.Background())
	cancel()

	if got := cache.get(gone, snapshotNow, time.Minute, reading.healthRead(false)); got != -100 {
		t.Fatalf("a check whose client left gets %d, want unavailable at once", got)
	}
	close(reading.gate)
	deadline := time.Now().Add(healthReadBudget)
	for {
		got := cache.get(context.Background(), snapshotNow, time.Minute, reading.healthRead(false))
		if got == 9 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the reading never landed: %d", got)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err, _ := reading.ctxErr.Load().(error); err != nil {
		t.Fatalf("the shared reading saw its context end with the client's: %v", err)
	}
	if calls := reading.calls.Load(); calls != 1 {
		t.Fatalf("%d readings, want the first to be kept", calls)
	}
}
