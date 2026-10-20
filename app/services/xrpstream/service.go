// Package xrpstream is the real-time XRP deposit listener: one long-running
// XRPL WebSocket subscription to the watched addresses of one XRP chain, run by
// the local workers when it is enabled. It records the validated payments it
// hears through the deposit scanner's own path, so the ledger scan, which keeps
// running beside it, stays the guarantee and records nothing twice.
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
)

// Event is one stream message: the validated ledger it is about (0 for none) and
// the native XRP payments it reports (a validated, tesSUCCESS Payment to another
// account, read like the ledger scan reads it).
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
	// AccountDeposits reads the validated native payments to account in ledgers
	// fromLedger..toLedger (account_tx).
	AccountDeposits(ctx context.Context, account string, fromLedger, toLedger uint64) ([]types.DetectedTransfer, error)
	// Next waits for the next stream message. A session that hears nothing for its
	// idle timeout fails.
	Next(ctx context.Context) (Event, error)
	Close() error
}

// Dialer opens a session to the configured endpoint.
type Dialer interface {
	Dial(ctx context.Context) (Session, error)
}

// AddressSource lists the active deposit addresses of a chain.
type AddressSource interface {
	PluckActiveAddresses(ctx context.Context, chainID string) ([]string, error)
}

// DepositRecorder records validated transfers as deposits through the scanner's
// path and confirms the new ones at head (deposit.Service).
type DepositRecorder interface {
	RecordValidatedTransfers(ctx context.Context, chainID string, head uint64, transfers []types.DetectedTransfer) (int, error)
}

// Service listens to one XRP chain. Run is its only entry point; its state (the
// newest validated ledger heard, a backfill left incomplete) lives in Run's
// goroutine.
type Service struct {
	chainID        string
	dialer         Dialer
	addresses      AddressSource
	deposits       DepositRecorder
	addressRefresh time.Duration
	reconnectMin   time.Duration
	reconnectMax   time.Duration

	// lastLedger is the newest validated ledger a session heard; a session that
	// follows backfills from it.
	lastLedger uint64
	// backfillFrom is the first ledger of a backfill that did not complete, 0 for
	// none; the next session starts its backfill there.
	backfillFrom uint64
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
