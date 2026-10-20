// Package xrpstream is the real-time XRP deposit listener: one long-running
// XRPL WebSocket subscription to the watched addresses of one XRP chain, run by
// the local workers when it is enabled. The stream is only a trigger: a validated
// payment it hears to a watched address makes the deposit service read that ledger
// through the chain's own RPC adapter, like the scanner reads it, so a stream on
// another network can never be the source of a deposit, and the ledger scan, which
// keeps running beside it, stays the guarantee and records nothing twice.
package xrpstream

import (
	"context"
	"time"

	"github.com/macrowallets/waas/pkg/types"
)

const (
	// DefaultAddressRefresh is how often the watched addresses are read again, so a
	// new deposit address is subscribed to.
	DefaultAddressRefresh = 15 * time.Second
	// DefaultReconnectMin and DefaultReconnectMax bound the reconnect backoff, which
	// doubles after each failed session and starts again once a session hears the
	// stream.
	DefaultReconnectMin = time.Second
	DefaultReconnectMax = time.Minute
	// MaxBackfillLedgers is how many ledgers a reconnect reads at most (the newest of
	// the gap, about a minute and a half); older ones are the ledger scan's.
	MaxBackfillLedgers = 20
	// maxLedgerAttempts is how many times a ledger whose read failed (the chain RPC
	// a ledger behind the stream) is read, once per new validated ledger; then it is
	// the ledger scan's. maxRetriedLedgers bounds the ledgers waiting for a retry.
	maxLedgerAttempts = 3
	maxRetriedLedgers = 100
)

// Event is one stream message: the validated ledger it is about (0 for none) and
// the native XRP payments it reports (a validated, tesSUCCESS Payment to another
// account). They only say which ledger to read: what is recorded comes from the
// chain RPC.
type Event struct {
	Ledger   uint64
	Deposits []types.DetectedTransfer
}

// Session is one XRPL WebSocket connection. Its calls are made from one goroutine;
// a call that fails leaves the session to be closed.
type Session interface {
	// SubscribeLedgers subscribes to the validated ledger stream and returns the
	// newest validated ledger.
	SubscribeLedgers(ctx context.Context) (uint64, error)
	// SubscribeAccounts adds accounts to the subscription: their validated
	// transactions are streamed.
	SubscribeAccounts(ctx context.Context, accounts []string) error
	// UnsubscribeAccounts removes accounts from the subscription.
	UnsubscribeAccounts(ctx context.Context, accounts []string) error
	// Next waits for the next stream message. A session that hears nothing for its
	// idle timeout fails.
	Next(ctx context.Context) (Event, error)
	Close() error
}

// Dialer opens a session to the configured endpoint, refusing one that serves
// another network than the chain's.
type Dialer interface {
	Dial(ctx context.Context) (Session, error)
}

// AddressSource lists the active deposit addresses of a chain.
type AddressSource interface {
	PluckActiveAddresses(ctx context.Context, chainID string) ([]string, error)
}

// DepositRecorder reads one validated ledger through the chain's RPC adapter,
// records its deposits through the scanner's path and confirms them
// (deposit.Service.RecordStreamedLedger).
type DepositRecorder interface {
	RecordStreamedLedger(ctx context.Context, chainID string, ledger uint64) (int, error)
}

// Service listens to one XRP chain. Run is its only entry point; its state (the
// newest validated ledger heard, the ledgers waiting for a retry) lives in Run's
// goroutine.
type Service struct {
	chainID        string
	dialer         Dialer
	addresses      AddressSource
	deposits       DepositRecorder
	addressRefresh time.Duration
	reconnectMin   time.Duration
	reconnectMax   time.Duration
	now            func() time.Time
	sleep          func(ctx context.Context, d time.Duration) bool

	// lastLedger is the newest validated ledger a session heard; the session that
	// follows reads the ledgers from it.
	lastLedger uint64
	// retries counts the reads of each ledger whose read failed.
	retries map[uint64]int
}

// Deps is everything the listener needs. A zero duration keeps its default.
type Deps struct {
	ChainID        string
	Dialer         Dialer
	Addresses      AddressSource
	Deposits       DepositRecorder
	AddressRefresh time.Duration
	ReconnectMin   time.Duration
	ReconnectMax   time.Duration
}

// NewService wires the listener from Deps.
func NewService(deps Deps) *Service {
	s := &Service{
		chainID:        deps.ChainID,
		dialer:         deps.Dialer,
		addresses:      deps.Addresses,
		deposits:       deps.Deposits,
		addressRefresh: orDefault(deps.AddressRefresh, DefaultAddressRefresh),
		reconnectMin:   orDefault(deps.ReconnectMin, DefaultReconnectMin),
		reconnectMax:   orDefault(deps.ReconnectMax, DefaultReconnectMax),
		now:            time.Now,
		sleep:          sleepContext,
		retries:        map[uint64]int{},
	}
	s.reconnectMax = max(s.reconnectMax, s.reconnectMin)
	return s
}

func orDefault(value, fallback time.Duration) time.Duration {
	if value <= 0 {
		return fallback
	}
	return value
}
