package evm

import (
	"strings"
	"sync"
)

// emptyLogMemory bounds the block hashes emptyLogAnswers keeps: past it, the
// counts start over (a block that keeps failing is then believed a little later).
const emptyLogMemory = 1024

// emptyLogAnswers counts, per block hash, the empty Transfer log answers the block's
// bloom contradicts.
type emptyLogAnswers struct {
	mu     sync.Mutex
	counts map[string]int
}

func newEmptyLogAnswers() *emptyLogAnswers {
	return &emptyLogAnswers{counts: map[string]int{}}
}

// count records one more empty answer for blockHash and returns how many it had.
func (e *emptyLogAnswers) count(blockHash string) int {
	if e == nil {
		return emptyBloomAnswersToAccept
	}
	key := strings.ToLower(blockHash)
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, known := e.counts[key]; !known && len(e.counts) >= emptyLogMemory {
		e.counts = map[string]int{}
	}
	e.counts[key]++
	return e.counts[key]
}

func (e *emptyLogAnswers) forget(blockHash string) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.counts, strings.ToLower(blockHash))
}
