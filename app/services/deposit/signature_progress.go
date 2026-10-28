package deposit

import (
	"cmp"
	"slices"
	"sync"
)

// signatureProgress is what this process's signature sync cycles learned of one
// chain, kept from one cycle to the next: the accounts of each owner as last listed,
// with the head read before that listing and when they were last all read, and how
// far each account is read up to. A cycle reads the owners it has time for, those
// read longest ago first (never read: first of all), so a chain with more accounts
// than one cycle reads is read in turns instead of from the same first owners every
// time, and an owner a cycle's budget cut short is among the first of the next. Its covered slot is the lowest slot any account
// is read up to, once every active owner is listed and the last read of every account
// settled it (read with proof down to its floor and up to a head): what an account is
// read up to stays true (a cursor never moves back), whichever cycle read it. A new
// process starts empty, so it claims nothing before it has read them all.
type signatureProgress struct {
	mu      sync.Mutex
	owners  map[string]ownerAccounts
	reads   map[string]accountRead
	skipped []string
	readSeq uint64
}

// ownerAccounts is the accounts of an owner as listed, the head read before the cycle
// that listed them (no account it gained since is in the list), and when a cycle last
// read them all, in the order owners were read (zero: never).
type ownerAccounts struct {
	accounts   []string
	listedHead uint64
	readSeq    uint64
}

// accountRead is how far an account is read: every signature up to coveredTo is
// recorded, from syncedFrom, its floor; settled says its last read proved that up to a
// head (not one that failed, stopped early, or left a gap or an end unproven).
type accountRead struct {
	coveredTo  uint64
	syncedFrom uint64
	settled    bool
}

// signatureCoverage is the progress of a chain's active owners after a cycle: how
// many accounts are known (an owner never listed counts as one), how many are not
// read up to a head yet, and, when every one is, the slot they are all read up to
// and the highest floor they were read from.
type signatureCoverage struct {
	accounts    int
	backfilling int
	head        uint64
	from        uint64
}

func newSignatureProgress() *signatureProgress {
	return &signatureProgress{owners: map[string]ownerAccounts{}, reads: map[string]accountRead{}}
}

// progressOf is the progress of chainID's signature sync in this process.
func (s *Service) progressOf(chainID string) *signatureProgress {
	progress, _ := s.signatureProgress.LoadOrStore(chainID, newSignatureProgress())
	return progress.(*signatureProgress)
}

// queue forgets the owners no longer active and returns the active ones (owners, in
// address order) in the order a cycle starts them: read longest ago first, those
// never read before all others, in address order among equals.
func (p *signatureProgress) queue(owners []string) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	active := make(map[string]bool, len(owners))
	for _, owner := range owners {
		active[owner] = true
	}
	for owner, listed := range p.owners {
		if active[owner] {
			continue
		}
		for _, account := range listed.accounts {
			delete(p.reads, account)
		}
		delete(p.owners, owner)
	}
	queue := slices.Clone(owners)
	slices.SortStableFunc(queue, func(a, b string) int {
		return cmp.Compare(p.owners[a].readSeq, p.owners[b].readSeq)
	})
	return queue
}

// listed records the accounts of owner, listed after head was read.
func (p *signatureProgress) listed(owner string, accounts []string, head uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	listed := p.owners[owner]
	listed.accounts, listed.listedHead = accounts, head
	p.owners[owner] = listed
}

// done records that a cycle read every account of owner, after every owner read
// before it.
func (p *signatureProgress) done(owner string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if listed, ok := p.owners[owner]; ok {
		p.readSeq++
		listed.readSeq = p.readSeq
		p.owners[owner] = listed
	}
}

// skippedMints records the registered mints the cycle's listings left out (the last
// cycle that listed an owner decides: the registry is the same for every owner) and
// returns them.
func (p *signatureProgress) skippedMints(listedAny bool, mints []string) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if listedAny {
		p.skipped = mints
	}
	return p.skipped
}

// read records how far a read took account. What an account is read up to only
// rises; whether it is settled is what its last read says.
func (p *signatureProgress) read(account string, result accountSync) {
	p.mu.Lock()
	defer p.mu.Unlock()
	known := p.reads[account]
	known.syncedFrom = result.syncedFrom
	known.coveredTo = max(known.coveredTo, result.coveredTo)
	known.settled = result.err == nil && result.coveredTo > 0
	p.reads[account] = known
}

// coverage is the progress of owners (the active ones) under the head read before
// they were listed (listedAt): the covered slot is never above it, nor above the
// head any owner's accounts were listed after, nor above what any account is read up
// to. failed are the accounts the cycle failed and the owners it could not list, not
// counted as backfilling.
func (p *signatureProgress) coverage(owners []string, listedAt uint64, failed map[string]bool) signatureCoverage {
	p.mu.Lock()
	defer p.mu.Unlock()
	cov := signatureCoverage{head: listedAt}
	complete := len(owners) > 0 && len(p.skipped) == 0
	counted := map[string]bool{}
	for _, owner := range owners {
		listed, ok := p.owners[owner]
		if !ok {
			cov.accounts++
			if !failed[owner] {
				cov.backfilling++
			}
			complete = false
			continue
		}
		cov.head = min(cov.head, listed.listedHead)
		for _, account := range listed.accounts {
			if counted[account] {
				continue
			}
			counted[account] = true
			cov.accounts++
			read := p.reads[account]
			if !read.settled {
				complete = false
				if !failed[account] {
					cov.backfilling++
				}
				continue
			}
			cov.head = min(cov.head, read.coveredTo)
			cov.from = max(cov.from, read.syncedFrom)
		}
	}
	if !complete {
		cov.head, cov.from = 0, 0
	}
	return cov
}
