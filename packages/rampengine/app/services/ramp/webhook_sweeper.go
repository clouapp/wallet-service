package ramp

import (
	"fmt"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
)

// WebhookSweeper re-queues stored webhook events that never reached the
// processor. The receiver answers 200 as soon as the row is stored, so a
// dispatch lost after that insert is one the provider will never redeliver.
//
// Only rows with no processed_at qualify. The processor stamps a failed apply
// too, so retrying one is an operator's decision in the admin explorer, not
// something a schedule does on its own.
type WebhookSweeper struct {
	events     repositories.WebhookEventRepository
	dispatcher EventDispatcher
}

func NewWebhookSweeper(events repositories.WebhookEventRepository, dispatcher EventDispatcher) *WebhookSweeper {
	return &WebhookSweeper{events: events, dispatcher: dispatcher}
}

// WebhookSweepReport is what one sweep did.
type WebhookSweepReport struct {
	Scanned    int
	Dispatched int
}

// Sweep queues up to limit unprocessed events received more than olderThan ago.
//
// olderThan keeps the sweep off events whose first dispatch is still waiting in
// the queue. What makes a re-dispatch safe is the processor's own guard, read
// under the event's lock before it applies anything; the window only spares the
// queue work that would end in a no-op.
func (s *WebhookSweeper) Sweep(olderThan time.Duration, limit int) (*WebhookSweepReport, error) {
	events, err := s.events.FindUnprocessed(time.Now().Add(-olderThan), limit)
	if err != nil {
		return nil, fmt.Errorf("ramp webhook sweep: list unprocessed: %w", err)
	}

	report := &WebhookSweepReport{Scanned: len(events)}
	for _, event := range events {
		if err := s.dispatcher.Dispatch(event.ID); err != nil {
			facades.Log().Errorf("ramp webhook sweep: dispatch event %d (%s): %v", event.ID, event.EventType, err)
			continue
		}
		report.Dispatched++
	}
	return report, nil
}
