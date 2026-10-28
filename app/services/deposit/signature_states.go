package deposit

import (
	"context"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/macrowallets/waas/app/services/chain"
)

// signatureOutboundLookback is how far back the first cycle of a process looks for
// the chain's outbound transfers; later cycles look back to the previous one's start.
const signatureOutboundLookback = time.Hour

// movedOwners finds, among owners, those to read before the rotation: an owner with
// an account whose state, read now in batches of chain.AccountStateBatch
// (settings.Concurrency batches at once, until the cycle's deadline), differs from the
// state stored after its last read (an account never read that exists, a transfer in
// or out since; no batch starts past the cycle's deadline), and an owner that sent a withdrawal, a sweep or a gas seed since the
// previous cycle began (a transfer out that a deposit of the same amount offsets
// leaves the state as it was). The accounts of an owner are those last listed, or,
// for an owner this process never listed, those it derives without a call. A batch or
// an owner that cannot be read flags nothing: the rotation still reads every account.
// Only an adapter that reads states (chain.AccountStateReader) is asked; another
// reads its owners in turn as before.
func (c *signatureCycle) movedOwners(ctx context.Context, owners []string, since time.Time) []string {
	reader, ok := c.reader.(chain.AccountStateReader)
	if !ok || len(owners) == 0 {
		return nil
	}
	ownerOf := map[string]string{}
	var accounts []string
	for _, owner := range owners {
		listed := c.progress.accountsOf(owner)
		if listed == nil {
			derived, err := reader.DerivedAccounts(ctx, owner)
			if err != nil {
				slog.Warn("deposit signature sync: the accounts of an address were not derived; it is read in turn",
					"chain", c.chainID, "address", owner, "error", err)
				continue
			}
			listed = derived
		}
		for _, account := range listed {
			if _, seen := ownerOf[account]; !seen {
				ownerOf[account] = owner
				accounts = append(accounts, account)
			}
		}
	}

	var mu sync.Mutex
	states, slot := make(map[string]string, len(accounts)), uint64(0)
	batches := slices.Collect(slices.Chunk(accounts, chain.AccountStateBatch))
	failed := 0
	forEachConcurrently(ctx, c.settings.Concurrency, len(batches), func(i int) {
		if !c.svc.now().Before(c.deadline) {
			return
		}
		batch, err := reader.AccountStates(ctx, batches[i])
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			failed++
			slog.Warn("deposit signature sync: account states not read; those accounts are read in turn",
				"chain", c.chainID, "accounts", len(batches[i]), "error", err, "provider_cause", chain.CauseText(err))
			return
		}
		if slot == 0 || batch.Slot < slot {
			slot = batch.Slot
		}
		maps.Copy(states, batch.States)
	})

	due := map[string]bool{}
	moved := 0
	for account, state := range states {
		stored := ""
		if row, ok := c.cursors[account]; ok {
			stored = row.AccountState
		}
		if c.progress.observe(account, state, stored, slot) {
			moved++
			due[ownerOf[account]] = true
		}
	}
	senders, err := c.svc.txRepo.OutboundSendersSince(ctx, c.chainID, since)
	if err != nil {
		slog.Warn("deposit signature sync: the chain's outbound transfers were not read; their senders are read in turn",
			"chain", c.chainID, "error", err)
	}
	watched := make(map[string]bool, len(owners))
	for _, owner := range owners {
		watched[owner] = true
	}
	for _, sender := range senders {
		if watched[sender] {
			due[sender] = true
		}
	}
	c.moved = moved
	var first []string
	for _, owner := range owners {
		if due[owner] {
			first = append(first, owner)
		}
	}
	slog.Info("deposit signature sync: account states read", "chain", c.chainID, "accounts", len(accounts),
		"read", len(states), "failed_batches", failed, "moved_accounts", moved, "addresses_first", len(first), "slot", slot)
	return first
}

// noteState stores, on an account read through, the state it moved to
// (signatureProgress.observe) once its newest signatures are proven read past the
// slot that state was first seen at (readTo): a later state that differs means it
// moved again. Until then the stored state stays as it was and the account keeps being
// read first, so a deposit an address index has not served yet (within the index
// margin) is read on a later cycle.
func (c *signatureCycle) noteState(st *accountState, readTo uint64) {
	state, slot, ok := c.progress.movedTo(st.account)
	if !ok || readTo < slot || state == st.state {
		return
	}
	st.state, st.stateSlot = state, slot
	st.unsaved++
}

// firstThen returns queue with first moved to its head, both in queue order.
func firstThen(queue, first []string) []string {
	if len(first) == 0 {
		return queue
	}
	ahead := make(map[string]bool, len(first))
	for _, owner := range first {
		ahead[owner] = true
	}
	ordered := slices.Clone(first)
	for _, owner := range queue {
		if !ahead[owner] {
			ordered = append(ordered, owner)
		}
	}
	return ordered
}
