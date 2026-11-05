package kyc

import (
	"fmt"
	"time"

	contractsqueue "github.com/goravel/framework/contracts/queue"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/support/queuejob"
)

// QueueDispatcher hands a submission id to the framework queue.
type QueueDispatcher struct {
	job queuejob.Deferred
}

// NewQueueDispatcher wires the dispatcher to a lazy job lookup.
func NewQueueDispatcher(resolve func() (contractsqueue.Job, error)) *QueueDispatcher {
	return &QueueDispatcher{job: resolve}
}

// Dispatch queues the submission.
func (d *QueueDispatcher) Dispatch(submissionID string, customerID uint, provider string) error {
	pending, err := d.pending(submissionID, customerID, provider)
	if err != nil {
		return err
	}
	return pending.Dispatch()
}

// RunsInline reports whether the default connection is the sync one.
func (d *QueueDispatcher) RunsInline() bool {
	return facades.Config().GetString("queue.default") == "sync"
}

// DispatchAfter queues the submission to run later, freeing the worker in the
// meantime. The arguments are the same ones: the job is identified by its
// staging id, so a re-queue is the same submission and not a second one.
func (d *QueueDispatcher) DispatchAfter(
	submissionID string, customerID uint, provider string, delay time.Duration,
) error {
	pending, err := d.pending(submissionID, customerID, provider)
	if err != nil {
		return err
	}
	return pending.Delay(time.Now().Add(delay)).Dispatch()
}

func (d *QueueDispatcher) pending(
	submissionID string, customerID uint, provider string,
) (contractsqueue.PendingJob, error) {
	pending, err := d.job.Stage(
		contractsqueue.Arg{Value: submissionID, Type: "string"},
		contractsqueue.Arg{Value: uint64(customerID), Type: "uint64"},
		contractsqueue.Arg{Value: provider, Type: "string"},
	)
	if err != nil {
		return nil, fmt.Errorf("kyc: %w", err)
	}
	return pending, nil
}

var _ Dispatcher = (*QueueDispatcher)(nil)
