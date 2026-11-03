// Package queuejob holds the plumbing a queue job repeats on both sides of the
// wire: reading the arguments the worker delivers back as `...any`, and staging
// a job whose instance can only be looked up at dispatch time.
package queuejob

import (
	"fmt"

	contractsqueue "github.com/goravel/framework/contracts/queue"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
)

// Uint reads args[i] as a uint.
//
// The dispatch side sets Type "uint64", Go literals from tests land as int or
// uint, and a queue store that round-trips its payload through JSON delivers
// every number as a float64 — so a job that asserts one of them panics the
// worker on the others. A negative input is rejected rather than converted:
// the conversion is implementation-defined and produces an id no row holds,
// which the worker then retries forever.
func Uint(args []any, i int) (uint, error) {
	if i < 0 || i >= len(args) {
		return 0, fmt.Errorf("want a number at args[%d], got %d argument(s)", i, len(args))
	}
	switch n := args[i].(type) {
	case uint:
		return n, nil
	case uint64:
		return uint(n), nil
	case int:
		return nonNegative(int64(n), args[i])
	case int64:
		return nonNegative(n, args[i])
	case float64:
		return nonNegative(int64(n), args[i])
	}
	return 0, fmt.Errorf("unsupported arg type %T", args[i])
}

func nonNegative(n int64, original any) (uint, error) {
	if n < 0 {
		return 0, fmt.Errorf("negative id %v", original)
	}
	return uint(n), nil
}

// Deferred looks a queue job up at dispatch time rather than holding it.
//
// The lookup is deferred because the job, the service it calls and the
// dispatcher that service holds form a cycle: the job needs the service, the
// service needs a dispatcher, and the dispatcher needs the job. Deferring it
// breaks the cycle without a setter that would leave the service half-built in
// between.
type Deferred func() (contractsqueue.Job, error)

// Stage resolves the job and stages it with args, for the caller to Dispatch or
// Delay. Only the args travel: the worker resolves the registered instance by
// signature, so anything set on the job struct here never reaches it.
func (resolve Deferred) Stage(args ...contractsqueue.Arg) (contractsqueue.PendingJob, error) {
	job, err := resolve()
	if err != nil {
		return nil, fmt.Errorf("resolve queue job: %w", err)
	}
	// Macro's adapter commits a durable tenant-scoped task instead of
	// assuming Xip's queue driver or running a provider inside the request.
	if native, ok := job.(interface {
		StageNative([]contractsqueue.Arg) (contractsqueue.PendingJob, error)
	}); ok {
		return native.StageNative(args)
	}
	return facades.Queue().Job(job, args), nil
}
