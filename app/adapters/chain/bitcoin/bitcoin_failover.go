package bitcoin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/macrowallets/waas/pkg/types"
)

const (
	// A provider that fails this many calls in a row is skipped for a cooldown that
	// starts at btcProviderCooldownBase and doubles on every new trip, up to
	// btcProviderCooldownMax. A success closes it again.
	btcProviderFailuresToOpen = 3
	btcProviderCooldownBase   = 30 * time.Second
	btcProviderCooldownMax    = 5 * time.Minute
	btcProviderMaxTripShift   = 8
)

// bitcoinFailover tries the providers of one network in order. Reads move to the
// next provider on any failure; a provider that keeps failing is skipped for a
// growing cooldown (circuit breaker), unless every provider is cooling down, in
// which case all are tried anyway. A broadcast only moves on when the provider did
// not decide on the transaction (transport error, 5xx, rate limit): the same signed
// bytes are sent, never a new signature, and a node answering "already known" means
// an earlier attempt was accepted.
type bitcoinFailover struct {
	chainID string
	members []*btcProviderMember
	now     func() time.Time
}

type btcProviderMember struct {
	provider bitcoinProvider

	mu                  sync.Mutex
	consecutiveFailures int
	trips               int
	openUntil           time.Time
}

func newBitcoinFailover(chainID string, providers ...bitcoinProvider) *bitcoinFailover {
	members := make([]*btcProviderMember, 0, len(providers))
	for _, provider := range providers {
		members = append(members, &btcProviderMember{provider: provider})
	}
	return &bitcoinFailover{chainID: chainID, members: members, now: time.Now}
}

func (m *btcProviderMember) available(now time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return !now.Before(m.openUntil)
}

func (m *btcProviderMember) succeeded() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.consecutiveFailures = 0
	m.trips = 0
	m.openUntil = time.Time{}
}

// failed counts a failure and reports the cooldown when it opened the breaker.
func (m *btcProviderMember) failed(now time.Time) time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.consecutiveFailures++
	if m.consecutiveFailures < btcProviderFailuresToOpen {
		return 0
	}
	cooldown := min(btcProviderCooldownBase<<min(m.trips, btcProviderMaxTripShift), btcProviderCooldownMax)
	m.trips++
	m.consecutiveFailures = 0
	m.openUntil = now.Add(cooldown)
	return cooldown
}

// ordered is the members to try: the available ones in configured order, or every
// member when all are cooling down.
func (f *bitcoinFailover) ordered() []*btcProviderMember {
	now := f.now()
	ready := make([]*btcProviderMember, 0, len(f.members))
	for _, member := range f.members {
		if member.available(now) {
			ready = append(ready, member)
		}
	}
	if len(ready) == 0 {
		return f.members
	}
	return ready
}

func (f *bitcoinFailover) blame(member *btcProviderMember, op string, err error, last bool) {
	cooldown := member.failed(f.now())
	attrs := []any{"chain", f.chainID, "op", op, "provider", member.provider.label(), "error", err}
	if cooldown > 0 {
		attrs = append(attrs, "skipped_for", cooldown.String())
	}
	if last {
		slog.Warn("btc provider failed", attrs...)
		return
	}
	slog.Warn("btc provider failed, trying the next one", attrs...)
}

// failoverRead runs call on each provider in turn and returns the first answer.
func failoverRead[T any](ctx context.Context, f *bitcoinFailover, op string, call func(bitcoinProvider) (T, error)) (T, error) {
	var zero T
	members := f.ordered()
	if len(members) == 1 {
		return call(members[0].provider)
	}
	var failures []error
	for i, member := range members {
		value, err := call(member.provider)
		if err == nil {
			member.succeeded()
			return value, nil
		}
		if ctx.Err() != nil {
			return zero, err
		}
		if errors.Is(err, errProviderUnsupported) {
			failures = append(failures, fmt.Errorf("%s: %w", member.provider.label(), err))
			continue
		}
		f.blame(member, op, err, i == len(members)-1)
		failures = append(failures, fmt.Errorf("%s: %w", member.provider.label(), err))
	}
	return zero, fmt.Errorf("btc %s: every provider failed: %w", op, errors.Join(failures...))
}

func (f *bitcoinFailover) balance(ctx context.Context, address string) (*types.Balance, error) {
	return failoverRead(ctx, f, "balance", func(p bitcoinProvider) (*types.Balance, error) { return p.balance(ctx, address) })
}

func (f *bitcoinFailover) confirmedUTXOs(ctx context.Context, address string) ([]btcInput, error) {
	return failoverRead(ctx, f, "utxos", func(p bitcoinProvider) ([]btcInput, error) { return p.confirmedUTXOs(ctx, address) })
}

func (f *bitcoinFailover) latestBlock(ctx context.Context) (uint64, error) {
	return failoverRead(ctx, f, "latest block", func(p bitcoinProvider) (uint64, error) { return p.latestBlock(ctx) })
}

func (f *bitcoinFailover) scanBlock(ctx context.Context, blockNum uint64) ([]types.DetectedTransfer, error) {
	return failoverRead(ctx, f, "scan block", func(p bitcoinProvider) ([]types.DetectedTransfer, error) { return p.scanBlock(ctx, blockNum) })
}

func (f *bitcoinFailover) transactionBlock(ctx context.Context, txID string) (uint64, error) {
	return failoverRead(ctx, f, "tx status", func(p bitcoinProvider) (uint64, error) { return p.transactionBlock(ctx, txID) })
}

func (f *bitcoinFailover) feeRate(ctx context.Context) (int64, error) {
	return failoverRead(ctx, f, "fee rate", func(p bitcoinProvider) (int64, error) { return p.feeRate(ctx) })
}

// broadcast sends raw to the first provider that decides on it. wantTxID, when known,
// is the txid computed locally: a provider answering another txid is an error, and an
// "already known" refusal is the success of an earlier attempt with these bytes.
func (f *bitcoinFailover) broadcast(ctx context.Context, raw []byte, wantTxID string) (string, error) {
	members := f.ordered()
	var failures []error
	for i, member := range members {
		txHash, err := member.provider.broadcast(ctx, raw)
		if err == nil {
			member.succeeded()
			return checkBroadcastTxID(member.provider.label(), strings.ToLower(strings.TrimSpace(txHash)), wantTxID)
		}
		var rejected *btcBroadcastRejectedError
		if errors.As(err, &rejected) {
			member.succeeded()
			if wantTxID != "" && isBTCAlreadyKnown(rejected.reason) {
				slog.Info("btc broadcast: transaction already known to the network", "chain", f.chainID, "provider", member.provider.label(), "txid", wantTxID)
				return wantTxID, nil
			}
			return "", err
		}
		if ctx.Err() != nil {
			return "", err
		}
		if errors.Is(err, errProviderUnsupported) {
			failures = append(failures, fmt.Errorf("%s: %w", member.provider.label(), err))
			continue
		}
		if len(members) > 1 {
			f.blame(member, "broadcast", err, i == len(members)-1)
		}
		failures = append(failures, fmt.Errorf("%s: %w", member.provider.label(), err))
	}
	if len(failures) == 1 {
		return "", failures[0]
	}
	return "", fmt.Errorf("btc broadcast: no provider accepted the transaction: %w", errors.Join(failures...))
}

func checkBroadcastTxID(provider, got, want string) (string, error) {
	if want == "" {
		return got, nil
	}
	if got != want {
		return "", fmt.Errorf("btc broadcast: %s answered txid %q, the signed transaction is %s", provider, got, want)
	}
	return got, nil
}
