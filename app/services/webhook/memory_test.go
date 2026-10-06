package webhook

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

// memoryWebhook is the config and event ports the webhook service already takes.
type memoryWebhook struct {
	mu          sync.Mutex
	configs     map[uuid.UUID]*models.WebhookConfig
	configOrder []uuid.UUID
	events      map[string]*models.WebhookEvent
	eventOrder  []string
	touched     map[string]time.Time
	findByIDErr error
}

func newMemoryWebhook() *memoryWebhook {
	return &memoryWebhook{
		configs: map[uuid.UUID]*models.WebhookConfig{},
		events:  map[string]*models.WebhookEvent{},
		touched: map[string]time.Time{},
	}
}

func newMemoryWebhookService() (*Service, *memoryWebhook) {
	store := newMemoryWebhook()
	return NewService(Deps{Configs: store, Events: memoryEvents{store: store}}), store
}

func newTestWebhookSvc() *Service {
	svc, _ := newMemoryWebhookService()
	return svc
}

func (m *memoryWebhook) Create(_ context.Context, cfg *models.WebhookConfig) error {
	if cfg == nil {
		return fmt.Errorf("create webhook config: config is nil")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.configs[cfg.ID]; ok {
		return fmt.Errorf("create webhook config: duplicate id")
	}
	copied := *cfg
	m.configs[cfg.ID] = &copied
	m.configOrder = append(m.configOrder, cfg.ID)
	return nil
}

func (m *memoryWebhook) FindActive(ctx context.Context) ([]models.WebhookConfig, error) {
	if err := m.readErr(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []models.WebhookConfig
	for _, id := range m.configOrder {
		cfg := m.configs[id]
		if cfg != nil && cfg.IsActive {
			out = append(out, *cfg)
		}
	}
	return out, nil
}

func (m *memoryWebhook) FindAll(context.Context) ([]models.WebhookConfig, error) {
	if err := m.readErr(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]models.WebhookConfig, 0, len(m.configOrder))
	for _, id := range m.configOrder {
		if cfg := m.configs[id]; cfg != nil {
			out = append(out, *cfg)
		}
	}
	return out, nil
}

func (m *memoryWebhook) FindVisibleToAccount(_ context.Context, accountID uuid.UUID) ([]models.WebhookConfig, error) {
	if err := m.readErr(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []models.WebhookConfig
	for _, id := range m.configOrder {
		cfg := m.configs[id]
		if cfg == nil || cfg.WalletID != nil {
			continue
		}
		if cfg.AccountID != nil && *cfg.AccountID != accountID {
			continue
		}
		out = append(out, *cfg)
	}
	return out, nil
}

func (m *memoryWebhook) FindByID(_ context.Context, id uuid.UUID) (*models.WebhookConfig, error) {
	if m.findByIDErr != nil {
		return nil, m.findByIDErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cfg, ok := m.configs[id]
	if !ok || cfg == nil {
		return nil, models.ErrRepositoryNotFound
	}
	copied := *cfg
	return &copied, nil
}

func (m *memoryWebhook) AssignAccount(_ context.Context, id, accountID uuid.UUID, events *string, isActive *bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cfg, ok := m.configs[id]
	if !ok || cfg == nil {
		return models.ErrRepositoryNotFound
	}
	owner := accountID
	cfg.AccountID = &owner
	if events != nil {
		cfg.Events = *events
	}
	if isActive != nil {
		cfg.IsActive = *isActive
	}
	return nil
}

func (m *memoryWebhook) DeleteByID(_ context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.configs, id)
	order := m.configOrder[:0]
	for _, existing := range m.configOrder {
		if existing != id {
			order = append(order, existing)
		}
	}
	m.configOrder = order
	return nil
}

func (m *memoryWebhook) setActive(id uuid.UUID, active bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cfg := m.configs[id]; cfg != nil {
		cfg.IsActive = active
	}
}

func (m *memoryWebhook) readErr() error { return nil }

func (m *memoryWebhook) CreateEvent(_ context.Context, event *models.WebhookEvent) error {
	return m.CreateEventRow(event)
}

// The event port method is Create. A second Create on this type is not allowed,
// so events are stored by the method below and the port is satisfied by embedding
// is not used. memoryWebhook.Create is the config port. Event Create is a
// different signature (*WebhookEvent vs *WebhookConfig), which Go still rejects
// as a duplicate name. The event store is memoryEvents.
func (m *memoryWebhook) CreateEventRow(event *models.WebhookEvent) error {
	if event == nil {
		return fmt.Errorf("create webhook event: event is nil")
	}
	if event.ID == uuid.Nil {
		event.ID = uuid.New()
	}
	if event.DeliveryStatus == "" {
		event.DeliveryStatus = models.WebhookDeliveryPending
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := event.ID.String()
	if _, ok := m.events[key]; ok {
		return fmt.Errorf("duplicate key value violates unique constraint")
	}
	copied := *event
	m.events[key] = &copied
	m.eventOrder = append(m.eventOrder, key)
	return nil
}

type memoryEvents struct{ store *memoryWebhook }

func (e memoryEvents) Create(ctx context.Context, event *models.WebhookEvent) error {
	return e.store.CreateEventRow(event)
}

func (e memoryEvents) AlreadyDelivered(_ context.Context, eventID string) (bool, error) {
	if eventID == "" {
		return false, fmt.Errorf("webhook event id is required")
	}
	e.store.mu.Lock()
	defer e.store.mu.Unlock()
	event, ok := e.store.events[eventID]
	if !ok || event == nil {
		return false, nil
	}
	return event.DeliveryStatus == models.WebhookDeliveryDelivered, nil
}

func (e memoryEvents) MarkDelivered(_ context.Context, eventID string) error {
	e.store.mu.Lock()
	defer e.store.mu.Unlock()
	event := e.store.events[eventID]
	if event == nil {
		return nil
	}
	now := time.Now().UTC()
	event.DeliveryStatus = models.WebhookDeliveryDelivered
	event.DeliveredAt = &now
	event.Attempts++
	return nil
}

func (e memoryEvents) IncrementAttempt(_ context.Context, eventID, errMsg string) error {
	e.store.mu.Lock()
	defer e.store.mu.Unlock()
	event := e.store.events[eventID]
	if event == nil {
		return nil
	}
	event.Attempts++
	event.LastError = errMsg
	e.store.touched[eventID] = time.Now().UTC()
	return nil
}

func (e memoryEvents) ExistsForSubject(_ context.Context, configID uuid.UUID, eventType, subjectID string) (bool, error) {
	if configID == uuid.Nil || eventType == "" || subjectID == "" {
		return false, fmt.Errorf("config id, event type and subject id are required")
	}
	e.store.mu.Lock()
	defer e.store.mu.Unlock()
	for _, event := range e.store.events {
		if event.WebhookConfigID != nil && *event.WebhookConfigID == configID && event.EventType == eventType && event.SubjectID != nil && *event.SubjectID == subjectID {
			return true, nil
		}
	}
	return false, nil
}

func (e memoryEvents) FindDueForDelivery(_ context.Context, limit int, baseBackoff, maxBackoff time.Duration) ([]models.WebhookEvent, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("limit must be positive")
	}
	if baseBackoff <= 0 || maxBackoff < baseBackoff {
		return nil, fmt.Errorf("backoff must be positive and max backoff must not be below the base")
	}
	e.store.mu.Lock()
	defer e.store.mu.Unlock()
	now := time.Now().UTC()
	var out []models.WebhookEvent
	for _, key := range e.store.eventOrder {
		event := e.store.events[key]
		if event == nil || event.DeliveryStatus != models.WebhookDeliveryPending || event.WebhookConfigID == nil || event.Attempts >= event.MaxAttempts {
			continue
		}
		if !backoffElapsed(event.Attempts, e.store.touched[key], baseBackoff, maxBackoff, now) {
			continue
		}
		out = append(out, *event)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func (e memoryEvents) MarkFailed(_ context.Context, eventID, errMsg string) error {
	e.store.mu.Lock()
	defer e.store.mu.Unlock()
	event := e.store.events[eventID]
	if event == nil {
		return fmt.Errorf("mark webhook event failed: missing %s", eventID)
	}
	event.DeliveryStatus = models.WebhookDeliveryFailed
	event.LastError = errMsg
	e.store.touched[eventID] = time.Now().UTC()
	return nil
}

func backoffElapsed(attempts int, updated time.Time, base, max time.Duration, now time.Time) bool {
	if attempts == 0 || updated.IsZero() {
		return true
	}
	seconds := base.Seconds() * math.Pow(2, float64(attempts-1))
	if seconds > max.Seconds() {
		seconds = max.Seconds()
	}
	deadline := updated.Add(time.Duration(seconds * float64(time.Second)))
	return !now.Before(deadline)
}

func (m *memoryWebhook) age(id uuid.UUID, attempts int, updated time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	event := m.events[id.String()]
	if event == nil {
		return
	}
	event.Attempts = attempts
	m.touched[id.String()] = updated
}

func (m *memoryWebhook) listEvents() []models.WebhookEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]models.WebhookEvent, 0, len(m.eventOrder))
	for _, key := range m.eventOrder {
		if event := m.events[key]; event != nil {
			out = append(out, *event)
		}
	}
	return out
}

func (m *memoryWebhook) addEvent(event *models.WebhookEvent) error {
	return memoryEvents{store: m}.Create(context.Background(), event)
}
