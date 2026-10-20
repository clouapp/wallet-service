package xrpstream

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"
)

// Run listens until ctx is cancelled: it connects, subscribes to every watched
// address and then to the validated ledger stream (in that order, so no ledger
// falls between the two), and reads, through the chain RPC, each ledger in which
// the stream reports a payment to a watched address. A session that fails (dial,
// network check, subscribe, read, or a silent endpoint) is closed and another one
// is opened after a backoff that doubles from ReconnectMin up to ReconnectMax and
// starts again only after a session that lived at least ReconnectMax, so an
// endpoint that accepts and drops at once is not dialed every second. Each session
// after the first reads the ledgers from the last one heard to the one its
// subscription starts at (at most MaxBackfillLedgers, the newest); what is older,
// and what the process missed before its first session, is the ledger scan's.
func (s *Service) Run(ctx context.Context) {
	wait := s.reconnectMin
	slog.Info("xrp deposit stream started", "chain", s.chainID, "address_refresh", s.addressRefresh.String())
	for {
		started := s.now()
		err := s.listen(ctx)
		if ctx.Err() != nil {
			slog.Info("xrp deposit stream stopped", "chain", s.chainID)
			return
		}
		if s.now().Sub(started) >= s.reconnectMax {
			wait = s.reconnectMin
		}
		slog.Warn("xrp deposit stream disconnected; reconnecting", "chain", s.chainID, "retry_in", wait.String(), "last_ledger", s.lastLedger, "error", err)
		if !s.sleep(ctx, wait) {
			slog.Info("xrp deposit stream stopped", "chain", s.chainID)
			return
		}
		wait = min(wait*2, s.reconnectMax)
	}
}

// listen runs one session until it fails.
func (s *Service) listen(ctx context.Context) error {
	session, err := s.dialer.Dial(ctx)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer func() {
		if closeErr := session.Close(); closeErr != nil {
			slog.Debug("xrp deposit stream close", "chain", s.chainID, "error", closeErr)
		}
	}()
	watched, err := s.follow(ctx, session, nil)
	if err != nil {
		return err
	}
	head, err := session.SubscribeLedgers(ctx)
	if err != nil {
		return fmt.Errorf("subscribe to validated ledgers: %w", err)
	}
	slog.Info("xrp deposit stream subscribed", "chain", s.chainID, "addresses", len(watched), "ledger", head)
	s.backfill(ctx, head)
	s.advance(head)

	refreshAt := s.now().Add(s.addressRefresh)
	for {
		if !s.now().Before(refreshAt) {
			if watched, err = s.follow(ctx, session, watched); err != nil {
				return err
			}
			refreshAt = s.now().Add(s.addressRefresh)
		}
		event, err := session.Next(ctx)
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}
		if event.Ledger > s.lastLedger {
			s.retryFailed(ctx)
		}
		for _, ledger := range depositLedgers(event, watched) {
			s.record(ctx, ledger)
		}
		s.advance(event.Ledger)
	}
}

// depositLedgers is the ledger of each payment of event to a watched address.
func depositLedgers(event Event, watched map[string]bool) []uint64 {
	var ledgers []uint64
	for _, deposit := range event.Deposits {
		if !watched[deposit.To] {
			continue
		}
		ledger := deposit.BlockNumber
		if ledger == 0 {
			ledger = event.Ledger
		}
		if ledger > 0 && (len(ledgers) == 0 || ledgers[len(ledgers)-1] != ledger) {
			ledgers = append(ledgers, ledger)
		}
	}
	return ledgers
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

// backfill reads, through the chain RPC, the ledgers from the last one heard to
// head (the gap of a reconnect), at most the newest MaxBackfillLedgers. A ledger
// that fails is retried like a streamed one (record).
func (s *Service) backfill(ctx context.Context, head uint64) {
	if s.lastLedger == 0 || head < s.lastLedger {
		return
	}
	from := s.lastLedger
	if head-from+1 > MaxBackfillLedgers {
		from = head - MaxBackfillLedgers + 1
		slog.Warn("xrp deposit stream: a long gap; its older ledgers are left to the ledger scan",
			"chain", s.chainID, "last_ledger", s.lastLedger, "from_ledger", from, "to_ledger", head)
	}
	for ledger := from; ledger <= head && ctx.Err() == nil; ledger++ {
		s.record(ctx, ledger)
	}
	slog.Info("xrp deposit stream backfilled", "chain", s.chainID, "from_ledger", from, "to_ledger", head)
}

// record has the deposit service read ledger through the chain RPC and record its
// deposits. A read that fails (the RPC behind the stream, an endpoint down) is
// tried again on the next validated ledgers, maxLedgerAttempts reads in all; then
// the ledger is the ledger scan's.
func (s *Service) record(ctx context.Context, ledger uint64) {
	recorded, err := s.deposits.RecordStreamedLedger(ctx, s.chainID, ledger)
	if err == nil {
		delete(s.retries, ledger)
		if recorded > 0 {
			slog.Info("xrp deposits recorded from the stream", "chain", s.chainID, "ledger", ledger, "count", recorded)
		}
		return
	}
	attempts := s.retries[ledger] + 1
	if attempts >= maxLedgerAttempts || (attempts == 1 && len(s.retries) >= maxRetriedLedgers) {
		delete(s.retries, ledger)
		slog.Warn("xrp deposit stream: ledger not read; the ledger scan records it", "chain", s.chainID, "ledger", ledger, "attempts", attempts, "error", err)
		return
	}
	s.retries[ledger] = attempts
	slog.Warn("xrp deposit stream: ledger not read; trying again on the next ledger", "chain", s.chainID, "ledger", ledger, "attempts", attempts, "error", err)
}

// retryFailed reads again, oldest first, the ledgers whose read failed.
func (s *Service) retryFailed(ctx context.Context) {
	if len(s.retries) == 0 {
		return
	}
	ledgers := make([]uint64, 0, len(s.retries))
	for ledger := range s.retries {
		ledgers = append(ledgers, ledger)
	}
	sort.Slice(ledgers, func(i, j int) bool { return ledgers[i] < ledgers[j] })
	for _, ledger := range ledgers {
		s.record(ctx, ledger)
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
