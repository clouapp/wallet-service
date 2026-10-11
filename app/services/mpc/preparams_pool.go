package mpc

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/bnb-chain/tss-lib/v2/ecdsa/keygen"
)

// preParamsRetryAfter is the pause after a failed generation before the next try.
const preParamsRetryAfter = 30 * time.Second

// PreParamsGenerator produces one set of ECDSA keygen pre-parameters.
type PreParamsGenerator func(ctx context.Context) (*keygen.LocalPreParams, error)

// GeneratePreParams is the production generator: the Paillier safe primes and the
// Ntilde parameters of one keygen party, searched by one goroutine so a background
// refill leaves the other cores to the requests.
func GeneratePreParams(ctx context.Context) (*keygen.LocalPreParams, error) {
	return keygen.GeneratePreParamsWithContext(ctx, 1)
}

// PreParamsPool keeps ECDSA keygen pre-parameters generated ahead of time, so a
// secp256k1 keygen does not search safe primes inside the request (minutes on a
// two-core host). The pre-parameters become part of the shares they are used in:
// they live only in this process's memory, are never logged or persisted, and each
// set is handed out once.
type PreParamsPool struct {
	ready      chan keygen.LocalPreParams
	slots      chan struct{} // one token per set the pool may still generate
	generate   PreParamsGenerator
	retryAfter time.Duration
	running    atomic.Bool
}

// NewPreParamsPool returns a pool that keeps up to size pre-parameters ready once Run
// is started. A size of 0 turns the pool off: Run returns at once and keygen
// generates its pre-parameters inline.
func NewPreParamsPool(size int, generate PreParamsGenerator) *PreParamsPool {
	size = max(size, 0)
	slots := make(chan struct{}, size)
	for range size {
		slots <- struct{}{}
	}
	return &PreParamsPool{
		ready:      make(chan keygen.LocalPreParams, size),
		slots:      slots,
		generate:   generate,
		retryAfter: preParamsRetryAfter,
	}
}

// Run fills the pool, and refills each set Take hands out, until ctx is cancelled.
func (p *PreParamsPool) Run(ctx context.Context) {
	if cap(p.slots) == 0 {
		return
	}
	p.running.Store(true)
	defer p.running.Store(false)
	for {
		select {
		case <-p.slots:
		case <-ctx.Done():
			return
		}
		params, err := p.generate(ctx)
		if err != nil {
			p.slots <- struct{}{}
			if ctx.Err() != nil {
				return
			}
			slog.Error("mpc pre-params generation failed", "error", err)
			select {
			case <-time.After(p.retryAfter):
				continue
			case <-ctx.Done():
				return
			}
		}
		p.ready <- *params
	}
}

// Take hands out one set of pre-parameters. While Run is running and the pool is
// empty it waits for the next set until ctx ends. It returns nil, nil when the pool
// is nil or not running, and the caller generates the pre-parameters itself.
func (p *PreParamsPool) Take(ctx context.Context) (*keygen.LocalPreParams, error) {
	if p == nil {
		return nil, nil
	}
	select {
	case params := <-p.ready:
		return p.handOut(params), nil
	default:
	}
	if !p.running.Load() {
		return nil, nil
	}
	select {
	case params := <-p.ready:
		return p.handOut(params), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *PreParamsPool) handOut(params keygen.LocalPreParams) *keygen.LocalPreParams {
	p.slots <- struct{}{}
	return &params
}
