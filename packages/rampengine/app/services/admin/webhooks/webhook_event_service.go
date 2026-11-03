package webhooks

import (
	"errors"
	"fmt"

	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/admin/webhooks"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	rampsvc "github.com/macrowallets/waas/packages/rampengine/app/services/ramp"
)

// ErrWebhookEventNotFound is returned when no event matches the explorer id.
var ErrWebhookEventNotFound = errors.New("webhook event not found")

// WebhookEventService is the business-logic boundary for the admin explorer.
type WebhookEventService struct {
	events     repositories.WebhookEventRepository
	dispatcher rampsvc.EventDispatcher
}

// NewWebhookEventService wires the explorer service.
func NewWebhookEventService(events repositories.WebhookEventRepository, dispatcher rampsvc.EventDispatcher) *WebhookEventService {
	return &WebhookEventService{events: events, dispatcher: dispatcher}
}

// List returns a paginated slice of events plus the total row count,
// normalizing page (>=1) and per-page (1..100, default 20).
func (s *WebhookEventService) List(d dto.AdminListWebhookEventsDTO) ([]models.WebhookEvent, int64, error) {
	filters := repositories.AdminWebhookEventListFilters{
		Provider:      d.Provider,
		EventType:     d.EventType,
		Processed:     d.Processed,
		TransactionID: d.TransactionID,
		ExternalRef:   d.ExternalRef,
		From:          d.From,
		To:            d.To,
		Page:          d.Page,
		PerPage:       d.PerPage,
		Sort:          d.Sort,
	}
	return s.events.AdminPaginate(filters)
}

// Get loads one event by internal id.
func (s *WebhookEventService) Get(id uint) (*models.WebhookEvent, error) {
	ev, err := s.events.FindByID(id)
	if err != nil {
		return nil, err
	}
	if ev == nil {
		return nil, ErrWebhookEventNotFound
	}
	return ev, nil
}

// ReprocessResult reports whether the event was handed to the queue.
type ReprocessResult struct {
	Dispatched bool
}

// Reprocess re-dispatches one event. An applied event is a no-op — the same
// guard `webhooks:reprocess` uses by only selecting processed_at IS NULL.
// Failed and never-processed rows go back on the queue.
//
// The check-and-dispatch runs under SELECT … FOR UPDATE inside a transaction,
// so two concurrent clicks cannot both pass it. The processor takes the same
// lock and repeats the check before applying, which is what actually keeps an
// event from being applied twice; this one spares the queue a round-trip that
// would end in a no-op.
func (s *WebhookEventService) Reprocess(id uint) (*ReprocessResult, error) {
	var dispatched bool
	txErr := s.events.WithLocked(id, func(ev *models.WebhookEvent, _ repositories.WebhookTx) error {
		if ev == nil {
			return ErrWebhookEventNotFound
		}
		if ev.Applied() {
			dispatched = false
			return nil
		}
		if err := s.dispatcher.Dispatch(id); err != nil {
			return fmt.Errorf("admin webhook events: reprocess dispatch: %w", err)
		}
		dispatched = true
		return nil
	})
	if txErr != nil {
		if errors.Is(txErr, ErrWebhookEventNotFound) {
			return nil, ErrWebhookEventNotFound
		}
		return nil, txErr
	}
	return &ReprocessResult{Dispatched: dispatched}, nil
}
