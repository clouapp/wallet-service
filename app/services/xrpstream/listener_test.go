package xrpstream

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sort"
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
	waitDeadline = 5 * time.Second
)

func payment(hash, to string, ledger uint64) types.DetectedTransfer {
	return types.DetectedTransfer{TxHash: hash, BlockNumber: ledger, From: "rJJWTfxZUg6FkYfcNHqrEgG9CUAFvnw7DB", To: to, Amount: big.NewInt(1_000_000), Asset: "xrp"}
}

// fakeNext is what one Next call answers.
type fakeNext struct {
	event Event
	err   error
}

// fakeSession is a scripted XRPL session: the test pushes what Next answers and
// reads, in order, the calls the listener made ("subscribe:a,b", "unsubscribe:a",
// "account_tx:a:from:to", "close").
type fakeSession struct {
	ledger      uint64
	next        chan fakeNext
	calls       chan string
	backfill    map[string][]types.DetectedTransfer
	backfillErr map[string]error
}

func newFakeSession(ledger uint64) *fakeSession {
	return &fakeSession{ledger: ledger, next: make(chan fakeNext), calls: make(chan string, 64),
		backfill: map[string][]types.DetectedTransfer{}, backfillErr: map[string]error{}}
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

func (s *fakeSession) AccountDeposits(_ context.Context, account string, from, to uint64) ([]types.DetectedTransfer, error) {
	s.calls <- fmt.Sprintf("account_tx:%s:%d:%d", account, from, to)
	return s.backfill[account], s.backfillErr[account]
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

// fakeDialer hands out its sessions in order, then refuses.
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

// recorded is one RecordValidatedTransfers call.
type recorded struct {
	head   uint64
	hashes string
}

type fakeRecorder struct{ calls chan recorded }

func (r *fakeRecorder) RecordValidatedTransfers(_ context.Context, chainID string, head uint64, transfers []types.DetectedTransfer) (int, error) {
	if chainID != testChain {
		return 0, fmt.Errorf("unexpected chain %s", chainID)
	}
	hashes := make([]string, 0, len(transfers))
	for _, transfer := range transfers {
		hashes = append(hashes, transfer.TxHash)
	}
	sort.Strings(hashes)
	r.calls <- recorded{head: head, hashes: strings.Join(hashes, ",")}
	return len(transfers), nil
}

func (r *fakeRecorder) expect(t *testing.T, want recorded) {
	t.Helper()
	select {
	case got := <-r.calls:
		if got != want {
			t.Fatalf("recorded %+v, want %+v", got, want)
		}
	case <-time.After(waitDeadline):
		t.Fatalf("nothing recorded, want %+v", want)
	}
}

type listenerFixture struct {
	dialer    *fakeDialer
	addresses *fakeAddresses
	recorder  *fakeRecorder
	stop      func()
}

// run starts the listener over sessions and returns when its first session is
// subscribed to the ledger stream.
func run(t *testing.T, refresh time.Duration, sessions ...*fakeSession) listenerFixture {
	t.Helper()
	f := listenerFixture{
		dialer:    &fakeDialer{sessions: sessions},
		addresses: &fakeAddresses{addresses: []string{addressA, addressB}},
		recorder:  &fakeRecorder{calls: make(chan recorded, 16)},
	}
	svc := NewService(Deps{
		ChainID: testChain, Dialer: f.dialer, Addresses: f.addresses, Deposits: f.recorder,
		AddressRefresh: refresh, ReconnectMin: time.Millisecond, ReconnectMax: 4 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.Run(ctx)
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
	return f
}

func TestRun_SubscribesTheWatchedAddressesAndRecordsAStreamedDeposit(t *testing.T) {
	session := newFakeSession(100)
	f := run(t, time.Hour, session)
	session.expect(t, "ledgers")
	session.expect(t, "subscribe:"+addressA+","+addressB)

	session.push(t, fakeNext{event: Event{Ledger: 101, Deposits: []types.DetectedTransfer{payment("H1", addressA, 101)}}})

	f.recorder.expect(t, recorded{head: 101, hashes: "H1"})
	session.push(t, fakeNext{event: Event{Ledger: 102}})
	f.stop()
	session.expect(t, "close")
	select {
	case call := <-session.calls:
		t.Fatalf("unexpected session call %q: the first session backfills nothing (the ledger scan covers the time before it)", call)
	default:
	}
}

func TestRun_ReconnectsAndBackfillsFromTheLastLedgerItHeard(t *testing.T) {
	first, second := newFakeSession(100), newFakeSession(110)
	second.backfill[addressA] = []types.DetectedTransfer{payment("H2", addressA, 107)}
	f := run(t, time.Hour, first, second)
	first.expect(t, "ledgers")
	first.expect(t, "subscribe:"+addressA+","+addressB)
	first.push(t, fakeNext{event: Event{Ledger: 105}})

	first.push(t, fakeNext{err: errors.New("connection reset by peer")})

	first.expect(t, "close")
	second.expect(t, "ledgers")
	second.expect(t, "subscribe:"+addressA+","+addressB)
	second.expect(t, "account_tx:"+addressA+":105:110")
	second.expect(t, "account_tx:"+addressB+":105:110")
	f.recorder.expect(t, recorded{head: 110, hashes: "H2"})
}

func TestRun_AnIncompleteBackfillStartsAgainFromItsFirstLedger(t *testing.T) {
	first, second, third := newFakeSession(100), newFakeSession(110), newFakeSession(130)
	second.backfillErr[addressA] = errors.New("lgrIdxsInvalid")
	f := run(t, time.Hour, first, second, third)
	first.expect(t, "ledgers")
	first.expect(t, "subscribe:"+addressA+","+addressB)
	first.push(t, fakeNext{err: errors.New("closed")})
	first.expect(t, "close")

	second.expect(t, "ledgers")
	second.expect(t, "subscribe:"+addressA+","+addressB)
	second.expect(t, "account_tx:"+addressA+":100:110")
	// The session goes on: the stream is not lost for a backfill that failed.
	second.push(t, fakeNext{event: Event{Ledger: 120, Deposits: []types.DetectedTransfer{payment("H3", addressB, 120)}}})
	f.recorder.expect(t, recorded{head: 120, hashes: "H3"})
	second.push(t, fakeNext{err: errors.New("closed")})
	second.expect(t, "close")

	third.expect(t, "ledgers")
	third.expect(t, "subscribe:"+addressA+","+addressB)
	third.expect(t, "account_tx:"+addressA+":100:130")
	third.expect(t, "account_tx:"+addressB+":100:130")
}

func TestRun_FollowsTheWatchedAddressesOnEachRefresh(t *testing.T) {
	session := newFakeSession(100)
	f := run(t, time.Nanosecond, session)
	session.expect(t, "ledgers")
	session.expect(t, "subscribe:"+addressA+","+addressB)

	f.addresses.set(addressB, addressC)
	session.push(t, fakeNext{event: Event{Ledger: 101}})

	session.expect(t, "subscribe:"+addressC)
	session.expect(t, "unsubscribe:"+addressA)
}

func TestRun_KeepsDialingUntilTheEndpointAnswers(t *testing.T) {
	session := newFakeSession(100)
	dialer := &fakeDialer{sessions: []*fakeSession{session}, refusals: 3}
	svc := NewService(Deps{
		ChainID: testChain, Dialer: dialer, Addresses: &fakeAddresses{addresses: []string{addressA}},
		Deposits: &fakeRecorder{calls: make(chan recorded, 1)}, ReconnectMin: time.Millisecond, ReconnectMax: 2 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.Run(ctx)
	}()

	session.expect(t, "ledgers")
	session.expect(t, "subscribe:"+addressA)
	cancel()
	<-done
	if dialer.dials != 4 {
		t.Fatalf("%d dials, want three refusals then the session", dialer.dials)
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
