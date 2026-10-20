package xrpstream

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/macrowallets/waas/pkg/types"
)

// Run listens until ctx is cancelled: it connects, subscribes to the validated
// ledger stream and to every watched address, and records each streamed deposit.
// A session that fails (dial, subscribe, read, or a silent endpoint) is closed and
// another one is opened after a backoff that doubles from ReconnectMin up to
// ReconnectMax, and starts again once a session hears the stream. Each session
// after the first backfills, with account_tx, the ledgers from the last one heard
// to the one its subscription starts at, so a reconnect loses nothing; what the
// process missed before its first session is the ledger scan's.
func (s *Service) Run(ctx context.Context) {
	wait := s.reconnectMin
	slog.Info("xrp deposit stream started", "chain", s.chainID, "address_refresh", s.addressRefresh.String())
	for {
		heard, err := s.listen(ctx)
		if ctx.Err() != nil {
			slog.Info("xrp deposit stream stopped", "chain", s.chainID)
			return
		}
		if heard {
			wait = s.reconnectMin
		}
		slog.Warn("xrp deposit stream disconnected; reconnecting", "chain", s.chainID, "retry_in", wait.String(), "last_ledger", s.lastLedger, "error", err)
		if !sleepContext(ctx, wait) {
			slog.Info("xrp deposit stream stopped", "chain", s.chainID)
			return
		}
		wait = min(wait*2, s.reconnectMax)
	}
}

// listen runs one session and reports whether it heard the stream before it
// ended, with the error that ended it.
func (s *Service) listen(ctx context.Context) (bool, error) {
	session, err := s.dialer.Dial(ctx)
	if err != nil {
		return false, fmt.Errorf("dial: %w", err)
	}
	defer func() {
		if closeErr := session.Close(); closeErr != nil {
			slog.Debug("xrp deposit stream close", "chain", s.chainID, "error", closeErr)
		}
	}()
	head, err := session.SubscribeLedgers(ctx)
	if err != nil {
		return false, fmt.Errorf("subscribe to validated ledgers: %w", err)
	}
	watched, err := s.follow(ctx, session, nil)
	if err != nil {
		return false, err
	}
	if from := s.backfillStart(); from > 0 && head >= from {
		s.backfill(ctx, session, watched, from, head)
	}
	s.advance(head)
	slog.Info("xrp deposit stream subscribed", "chain", s.chainID, "addresses", len(watched), "ledger", head)

	refreshAt := time.Now().Add(s.addressRefresh)
	heard := false
	for {
		if !time.Now().Before(refreshAt) {
			if watched, err = s.follow(ctx, session, watched); err != nil {
				return heard, err
			}
			refreshAt = time.Now().Add(s.addressRefresh)
		}
		event, err := session.Next(ctx)
		if err != nil {
			return heard, fmt.Errorf("read: %w", err)
		}
		heard = true
		if len(event.Deposits) > 0 {
			s.record(ctx, max(event.Ledger, s.lastLedger), event.Deposits)
		}
		s.advance(event.Ledger)
	}
}

// follow reads the watched addresses and moves the subscription to them: the new
// ones are subscribed to, the ones no longer active unsubscribed. A read that
// fails keeps the current subscription (the first one of a session fails it). A
// subscription request that fails ends the session.
func (s *Service) follow(ctx context.Context, session Session, current map[string]bool) (map[string]bool, error) {
	addresses, err := s.addresses.PluckActiveAddresses(ctx, s.chainID)
	if err != nil {
		if current == nil {
			return nil, fmt.Errorf("read watched addresses: %w", err)
		}
		slog.Warn("xrp deposit stream: watched addresses not read again; keeping the subscription", "chain", s.chainID, "error", err)
		return current, nil
	}
	next := make(map[string]bool, len(addresses))
	for _, address := range addresses {
		next[address] = true
	}
	added, removed := difference(next, current), difference(current, next)
	if len(added) > 0 {
		if err := session.SubscribeAccounts(ctx, added); err != nil {
			return current, fmt.Errorf("subscribe to %d addresses: %w", len(added), err)
		}
	}
	if len(removed) > 0 {
		if err := session.UnsubscribeAccounts(ctx, removed); err != nil {
			return current, fmt.Errorf("unsubscribe from %d addresses: %w", len(removed), err)
		}
	}
	if current != nil && len(added)+len(removed) > 0 {
		slog.Info("xrp deposit stream subscription updated", "chain", s.chainID, "added", len(added), "removed", len(removed), "addresses", len(next))
	}
	return next, nil
}

// difference is the members of a missing from b, sorted.
func difference(a, b map[string]bool) []string {
	var missing []string
	for member := range a {
		if !b[member] {
			missing = append(missing, member)
		}
	}
	sort.Strings(missing)
	return missing
}

// backfillStart is the first ledger the next backfill reads: where an incomplete
// one started, or the last ledger heard; 0 before any session heard one.
func (s *Service) backfillStart() uint64 {
	if s.backfillFrom > 0 {
		return s.backfillFrom
	}
	return s.lastLedger
}

// backfill records, for every watched address, the deposits of ledgers from..to
// (account_tx). The first address that cannot be read stops it: the next session
// starts again from the same ledger, and the ledger scan covers the gap meanwhile.
func (s *Service) backfill(ctx context.Context, session Session, watched map[string]bool, from, to uint64) {
	accounts := difference(watched, nil)
	found := 0
	for _, account := range accounts {
		deposits, err := session.AccountDeposits(ctx, account, from, to)
		if err != nil {
			s.backfillFrom = from
			slog.Warn("xrp deposit stream backfill incomplete; the next session reads it again and the ledger scan covers it",
				"chain", s.chainID, "from_ledger", from, "to_ledger", to, "error", err)
			return
		}
		if len(deposits) > 0 {
			found += len(deposits)
			s.record(ctx, to, deposits)
		}
	}
	s.backfillFrom = 0
	slog.Info("xrp deposit stream backfilled", "chain", s.chainID, "from_ledger", from, "to_ledger", to, "addresses", len(accounts), "deposits", found)
}

// record hands the deposits to the scanner's recording path. A failure is logged:
// the ledger scan records them.
func (s *Service) record(ctx context.Context, head uint64, deposits []types.DetectedTransfer) {
	recorded, err := s.deposits.RecordValidatedTransfers(ctx, s.chainID, head, deposits)
	if err != nil {
		slog.Warn("xrp deposit stream: deposits not recorded; the ledger scan records them", "chain", s.chainID, "ledger", head, "error", err)
	}
	if recorded > 0 {
		slog.Info("xrp deposits recorded from the stream", "chain", s.chainID, "ledger", head, "count", recorded)
	}
}

func (s *Service) advance(ledger uint64) {
	if ledger > s.lastLedger {
		s.lastLedger = ledger
	}
}

// sleepContext waits d, or reports false when ctx ends first.
func sleepContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
