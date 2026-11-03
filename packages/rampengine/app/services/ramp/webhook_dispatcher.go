package ramp

import (
	"fmt"

	contractsqueue "github.com/goravel/framework/contracts/queue"

	"github.com/macrowallets/waas/packages/rampengine/app/support/queuejob"
)

// EventDispatcher queues one stored webhook event for the processor.
// *QueueDispatcher satisfies this; tests inject a mockery mock.
type EventDispatcher interface {
	Dispatch(eventID uint) error
}

// QueueDispatcher hands a webhook_events row id to the ramp processor job.
type QueueDispatcher struct {
	job queuejob.Deferred
}

func NewQueueDispatcher(resolve func() (contractsqueue.Job, error)) *QueueDispatcher {
	return &QueueDispatcher{job: resolve}
}

// Dispatch queues the event. The payload never travels through the job: it
// already lives on the row.
func (d *QueueDispatcher) Dispatch(eventID uint) error {
	pending, err := d.job.Stage(contractsqueue.Arg{Value: uint64(eventID), Type: "uint64"})
	if err != nil {
		return fmt.Errorf("ramp webhook: %w", err)
	}
	return pending.Dispatch()
}

var _ EventDispatcher = (*QueueDispatcher)(nil)
