// Package tssfixture holds secp256k1 keygen pre-parameters (Paillier keys and safe
// primes) generated once for the test suites, so a test keygen does not search for
// safe primes (minutes on a CI runner). They are test material only: production
// keygens generate their own (mpc.PreParamsPool, or inside the keygen), each set used
// by one party of one keygen. Regenerate with
// `go test -tags generate_preparams ./tests/tssfixture/`.
package tssfixture

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/bnb-chain/tss-lib/v2/ecdsa/keygen"
)

//go:embed testdata/preparams.json
var raw []byte

var (
	loadOnce sync.Once
	sets     []keygen.LocalPreParams
	loadErr  error
	next     atomic.Uint64
)

func load() ([]keygen.LocalPreParams, error) {
	loadOnce.Do(func() {
		if err := json.Unmarshal(raw, &sets); err != nil {
			loadErr = fmt.Errorf("tssfixture: decode pre-params: %w", err)
			return
		}
		if len(sets) < 2 {
			loadErr = fmt.Errorf("tssfixture: want at least 2 pre-param sets, got %d", len(sets))
		}
	})
	return sets, loadErr
}

// Generator hands out the stored sets in turn (two in a row are always distinct),
// as an mpc.PreParamsGenerator.
func Generator() func(ctx context.Context) (*keygen.LocalPreParams, error) {
	return func(context.Context) (*keygen.LocalPreParams, error) {
		all, err := load()
		if err != nil {
			return nil, err
		}
		set := all[int(next.Add(1)-1)%len(all)]
		return &set, nil
	}
}
