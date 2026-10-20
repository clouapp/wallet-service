package xrpstream

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/macrowallets/waas/pkg/types"
)

const (
	testChain    = "txrp"
	addressA     = "rai1iQmERbDPvfuk31XVsYs6ZXeA8QRZnC"
	addressB     = "rwpNHW66K4YcwAGyx7F1ehUW12Yujwy7Ni"
	addressC     = "rDWd81gTyMMA1arNv9bH1qcBDN4EaozN1Y"
	unwatched    = "rJJWTfxZUg6FkYfcNHqrEgG9CUAFvnw7DB"
	waitDeadline = 5 * time.Second
)

func payment(hash, to string, ledger uint64) types.DetectedTransfer {
	return types.DetectedTransfer{TxHash: hash, BlockNumber: ledger, From: unwatched, To: to, Amount: big.NewInt(1_000_000), Asset: "xrp"}
}

// fakeNext is what one Next call answers.
type fakeNext struct {
	event Event
	err   error
}

// fakeSession is a scripted XRPL session: the test pushes what Next answers and
// reads, in order, the calls the listener made ("subscribe:a,b", "unsubscribe:a",
// "ledgers", "close").
type fakeSession struct {
	ledger uint64
	next   chan fakeNext
	calls  chan string
}

func newFakeSession(ledger uint64) *fakeSession {
	return &fakeSession{ledger: ledger, next: make(chan fakeNext), calls: make(chan string, 64)}
}

func (s *fakeSession) SubscribeLedgers(context.Context) (uint64, error) {
	s.calls <- "ledgers"
	return s.ledger, nil
}

func (s *fakeSession) SubscribeAccounts(_ context.Context, accounts []string) error {
	s.calls <- "subscribe:" + strings.Join(accounts, ",")
	return nil
}

func (s *fakeSession) UnsubscribeAccounts(_ context.Context, accounts []string) error {
	s.calls <- "unsubscribe:" + strings.Join(accounts, ",")
	return nil
}

func (s *fakeSession) Next(ctx context.Context) (Event, error) {
	select {
	case answer := <-s.next:
		return answer.event, answer.err
	case <-ctx.Done():
		return Event{}, ctx.Err()
	}
}

func (s *fakeSession) Close() error {
	s.calls <- "close"
	return nil
}

// push hands Next an event, failing the test if the listener stops reading.
func (s *fakeSession) push(t *testing.T, answer fakeNext) {
	t.Helper()
	select {
	case s.next <- answer:
	case <-time.After(waitDeadline):
		t.Fatal("the listener is not reading the session")
	}
}

func (s *fakeSession) expect(t *testing.T, want string) {
	t.Helper()
	select {
	case got := <-s.calls:
		if got != want {
			t.Fatalf("session call %q, want %q", got, want)
		}
	case <-time.After(waitDeadline):
		t.Fatalf("no session call, want %q", want)
	}
}

// subscribed waits for the session's subscription: the addresses first, then the
// ledger stream, so no ledger falls between the two.
func (s *fakeSession) subscribed(t *testing.T, addresses ...string) {
	t.Helper()
	s.expect(t, "subscribe:"+strings.Join(addresses, ","))
	s.expect(t, "ledgers")
}

// fakeDialer hands out its sessions in order, after refusals refused dials.
type fakeDialer struct {
	mu       sync.Mutex
	sessions []*fakeSession
	refusals int
	dials    int
}

func (d *fakeDialer) Dial(context.Context) (Session, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.dials++
	if d.refusals > 0 {
		d.refusals--
		return nil, errors.New("connection refused")
	}
	if len(d.sessions) == 0 {
		return nil, errors.New("no session left")
	}
	session := d.sessions[0]
	d.sessions = d.sessions[1:]
	return session, nil
}

type fakeAddresses struct {
	mu        sync.Mutex
	addresses []string
}

func (a *fakeAddresses) PluckActiveAddresses(_ context.Context, chainID string) ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if chainID != testChain {
		return nil, fmt.Errorf("unexpected chain %s", chainID)
	}
	return append([]string(nil), a.addresses...), nil
}

func (a *fakeAddresses) set(addresses ...string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.addresses = addresses
}

// fakeRecorder records the ledgers the listener asks to read; failing makes a
// ledger's read fail.
type fakeRecorder struct {
	mu      sync.Mutex
	ledgers chan uint64
	failing map[uint64]bool
}

func newFakeRecorder() *fakeRecorder {
	return &fakeRecorder{ledgers: make(chan uint64, 128), failing: map[uint64]bool{}}
}

func (r *fakeRecorder) RecordStreamedLedger(_ context.Context, chainID string, ledger uint64) (int, error) {
	if chainID != testChain {
		return 0, fmt.Errorf("unexpected chain %s", chainID)
	}
	r.ledgers <- ledger
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failing[ledger] {
		return 0, errors.New("ledger past the chain head")
	}
	return 1, nil
}

func (r *fakeRecorder) fail(ledger uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failing[ledger] = true
}

func (r *fakeRecorder) expect(t *testing.T, want ...uint64) {
	t.Helper()
	for _, ledger := range want {
		select {
		case got := <-r.ledgers:
			if got != ledger {
				t.Fatalf("recorded ledger %d, want %d", got, ledger)
			}
		case <-time.After(waitDeadline):
			t.Fatalf("no ledger recorded, want %d", ledger)
		}
	}
}

func (r *fakeRecorder) none(t *testing.T) {
	t.Helper()
	select {
	case got := <-r.ledgers:
		t.Fatalf("recorded ledger %d, want none", got)
	default:
	}
}

type listenerFixture struct {
	svc       *Service
	dialer    *fakeDialer
	addresses *fakeAddresses
	recorder  *fakeRecorder
	stop      func()
}

func newListener(refresh time.Duration, dialer *fakeDialer) *listenerFixture {
	f := &listenerFixture{dialer: dialer, addresses: &fakeAddresses{addresses: []string{addressA, addressB}}, recorder: newFakeRecorder()}
	f.svc = NewService(Deps{
		ChainID: testChain, Dialer: f.dialer, Addresses: f.addresses, Deposits: f.recorder,
		AddressRefresh: refresh, ReconnectMin: time.Millisecond, ReconnectMax: 4 * time.Millisecond,
	})
	return f
}

// start runs the listener until the test ends.
func (f *listenerFixture) start(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.svc.Run(ctx)
	}()
	f.stop = func() {
		cancel()
		select {
		case <-done:
		case <-time.After(waitDeadline):
			t.Fatal("Run did not return after its context was cancelled")
		}
	}
	t.Cleanup(f.stop)
}

func run(t *testing.T, refresh time.Duration, sessions ...*fakeSession) *listenerFixture {
	t.Helper()
	f := newListener(refresh, &fakeDialer{sessions: sessions})
	f.start(t)
	return f
}

func TestRun_ReadsTheLedgerOfADepositToAWatchedAddress(t *testing.T) {
	session := newFakeSession(100)
	f := run(t, time.Hour, session)
	session.subscribed(t, addressA, addressB)

	session.push(t, fakeNext{event: Event{Ledger: 101, Deposits: []types.DetectedTransfer{payment("H1", addressA, 101)}}})
	f.recorder.expect(t, 101)

	// A payment the subscription streams to an address nobody watches (a
	// withdrawal of the wallet) reads nothing.
	session.push(t, fakeNext{event: Event{Ledger: 102, Deposits: []types.DetectedTransfer{payment("H2", unwatched, 102)}}})
	session.push(t, fakeNext{event: Event{Ledger: 103, Deposits: []types.DetectedTransfer{payment("H3", addressB, 103)}}})
	f.recorder.expect(t, 103)
	f.stop()
	session.expect(t, "close")
	f.recorder.none(t)
}

func TestRun_ReadsEachLedgerSinceTheLastHeardAfterAReconnect(t *testing.T) {
	first, second := newFakeSession(100), newFakeSession(108)
	f := run(t, time.Hour, first, second)
	first.subscribed(t, addressA, addressB)
	first.push(t, fakeNext{event: Event{Ledger: 105}})

	first.push(t, fakeNext{err: errors.New("connection reset by peer")})

	first.expect(t, "close")
	second.subscribed(t, addressA, addressB)
	f.recorder.expect(t, 105, 106, 107, 108)
	f.recorder.none(t)
}

func TestRun_ReadsAtMostTheLastLedgersOfALongGap(t *testing.T) {
	head := uint64(100 + 3*MaxBackfillLedgers)
	first, second := newFakeSession(100), newFakeSession(head)
	f := run(t, time.Hour, first, second)
	first.subscribed(t, addressA, addressB)
	first.push(t, fakeNext{err: errors.New("closed")})

	second.subscribed(t, addressA, addressB)
	for ledger := head - MaxBackfillLedgers + 1; ledger <= head; ledger++ {
		f.recorder.expect(t, ledger)
	}
	f.recorder.none(t)
}

func TestRun_RetriesALedgerThatFailedOnTheNextLedgersThenLeavesItToTheScan(t *testing.T) {
	session := newFakeSession(100)
	f := run(t, time.Hour, session)
	f.recorder.fail(101)
	session.subscribed(t, addressA, addressB)

	session.push(t, fakeNext{event: Event{Ledger: 101, Deposits: []types.DetectedTransfer{payment("H1", addressA, 101)}}})
	for ledger := uint64(102); ledger <= 105; ledger++ {
		session.push(t, fakeNext{event: Event{Ledger: ledger}})
	}
	session.push(t, fakeNext{event: Event{Ledger: 106, Deposits: []types.DetectedTransfer{payment("H2", addressA, 106)}}})

	f.recorder.expect(t, 101, 101, 101, 106)
	f.recorder.none(t)
}

func TestRun_FollowsTheWatchedAddressesOnEachRefresh(t *testing.T) {
	session := newFakeSession(100)
	f := run(t, time.Nanosecond, session)
	session.subscribed(t, addressA, addressB)

	f.addresses.set(addressB, addressC)
	session.push(t, fakeNext{event: Event{Ledger: 101}})

	session.expect(t, "subscribe:"+addressC)
	session.expect(t, "unsubscribe:"+addressA)
}

// fakeTime is the listener's clock and sleep: the test moves the clock, and each
// wait is recorded and returns at once.
type fakeTime struct {
	mu    sync.Mutex
	at    time.Time
	waits []time.Duration
}

func (c *fakeTime) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *fakeTime) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

func (c *fakeTime) sleep(ctx context.Context, d time.Duration) bool {
	c.mu.Lock()
	c.waits = append(c.waits, d)
	c.mu.Unlock()
	return ctx.Err() == nil
}

func (c *fakeTime) slept() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.waits...)
}

func TestRun_TheBackoffStartsAgainOnlyAfterASessionLivedTheLongestWait(t *testing.T) {
	long, short, last := newFakeSession(100), newFakeSession(100), newFakeSession(100)
	f := newListener(time.Hour, &fakeDialer{sessions: []*fakeSession{long, short, last}, refusals: 4})
	f.svc.reconnectMin, f.svc.reconnectMax = time.Second, 8*time.Second
	clock := &fakeTime{at: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)}
	f.svc.now, f.svc.sleep = clock.now, clock.sleep
	f.start(t)

	long.subscribed(t, addressA, addressB)
	clock.advance(10 * time.Second)
	long.push(t, fakeNext{err: errors.New("closed")})
	short.subscribed(t, addressA, addressB)
	short.push(t, fakeNext{event: Event{Ledger: 101}})
	clock.advance(2 * time.Second)
	short.push(t, fakeNext{err: errors.New("closed")})
	last.subscribed(t, addressA, addressB)

	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, time.Second, 2 * time.Second}
	if got := clock.slept(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("waits %v, want %v: doubling while the endpoint refuses, starting again after a session that lived the longest wait, not after one that only heard a message", got, want)
	}
}

func TestRun_KeepsDialingUntilTheEndpointAnswers(t *testing.T) {
	session := newFakeSession(100)
	f := newListener(time.Hour, &fakeDialer{sessions: []*fakeSession{session}, refusals: 3})
	f.start(t)

	session.subscribed(t, addressA, addressB)
	f.stop()
	if f.dialer.dials != 4 {
		t.Fatalf("%d dials, want three refusals then the session", f.dialer.dials)
	}
}

func TestNew_Service_KeepsTheDefaultsForZeroDurations(t *testing.T) {
	svc := NewService(Deps{ChainID: testChain})
	if svc.addressRefresh != DefaultAddressRefresh || svc.reconnectMin != DefaultReconnectMin || svc.reconnectMax != DefaultReconnectMax {
		t.Fatalf("refresh %s reconnect %s..%s", svc.addressRefresh, svc.reconnectMin, svc.reconnectMax)
	}
	if bounded := NewService(Deps{ReconnectMin: time.Minute, ReconnectMax: time.Second}); bounded.reconnectMax != time.Minute {
		t.Fatalf("a maximum below the minimum is raised to it, got %s", bounded.reconnectMax)
	}
}
