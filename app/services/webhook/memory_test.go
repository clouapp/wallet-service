package webhook

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
)

// memoryWebhook is the config and event ports the webhook service already takes.
type memoryWebhook struct {
	mu          sync.Mutex
	configs     map[uuid.UUID]*models.WebhookConfig
	configOrder []uuid.UUID
	events      map[string]*models.WebhookEvent
	eventOrder  []string
	created     map[string]time.Time
	touched     map[string]time.Time
	// owners is the account of each event's wallet, which the repository reads
	// from the event's transaction or payload. An event missing here has no
	// wallet the store can tell.
	owners      map[string]uuid.UUID
	findByIDErr error
}

func newMemoryWebhook() *memoryWebhook {
	return &memoryWebhook{
		configs: map[uuid.UUID]*models.WebhookConfig{},
		events:  map[string]*models.WebhookEvent{},
		created: map[string]time.Time{},
		touched: map[string]time.Time{},
		owners:  map[string]uuid.UUID{},
	}
}

func newMemoryWebhookService() (*Service, *memoryWebhook) {
	store := newMemoryWebhook()
	return NewService(Deps{Configs: store, Events: memoryEvents{store: store}, Destinations: testDestinations()}), store
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
	m.created[key] = time.Now().UTC()
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

// SupersededByDepositFailure holds a deposit progress event of a transaction that
// has a deposit.failed event (the memory store has no transactions table).
func (e memoryEvents) SupersededByDepositFailure(_ context.Context, eventID string) (bool, error) {
	if eventID == "" {
		return false, fmt.Errorf("webhook event id is required")
	}
	e.store.mu.Lock()
	defer e.store.mu.Unlock()
	event := e.store.events[eventID]
	if event == nil || event.TransactionID == nil || event.EventType == string(types.EventDepositFailed) ||
		!strings.HasPrefix(event.EventType, "deposit.") {
		return false, nil
	}
	for _, other := range e.store.events {
		if other.TransactionID != nil && *other.TransactionID == *event.TransactionID && other.EventType == string(types.EventDepositFailed) {
			return true, nil
		}
	}
	return false, nil
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

// ClaimStaleForRedispatch mirrors the repository: pending rows of an active config
// with attempts left, untouched since staleAfter, oldest first; each claim counts
// one attempt and touches the row.
func (e memoryEvents) ClaimStaleForRedispatch(_ context.Context, staleAfter time.Duration, limit int) ([]models.WebhookEvent, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("limit must be positive")
	}
	if staleAfter <= 0 {
		return nil, fmt.Errorf("stale period must be positive")
	}
	e.store.mu.Lock()
	defer e.store.mu.Unlock()
	now := time.Now().UTC()
	cutoff := now.Add(-staleAfter)
	var out []models.WebhookEvent
	for _, key := range e.store.eventOrder {
		event := e.store.events[key]
		if event == nil || event.DeliveryStatus != models.WebhookDeliveryPending || event.WebhookConfigID == nil || event.Attempts >= event.MaxAttempts {
			continue
		}
		if cfg := e.store.configs[*event.WebhookConfigID]; cfg == nil || !cfg.IsActive {
			continue
		}
		updated := e.store.touched[key]
		if updated.IsZero() {
			updated = e.store.created[key]
		}
		if e.store.created[key].After(cutoff) || updated.After(cutoff) {
			continue
		}
		event.Attempts++
		e.store.touched[key] = now
		out = append(out, *event)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

// FindForRedelivery mirrors the repository: the config's events about a wallet of
// accountID created in [since, until), of eventTypes when any are given, oldest first.
func (e memoryEvents) FindForRedelivery(_ context.Context, configID, accountID uuid.UUID, since, until time.Time, eventTypes []string, limit int) ([]uuid.UUID, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("limit must be positive")
	}
	e.store.mu.Lock()
	defer e.store.mu.Unlock()
	wanted := map[string]bool{}
	for _, eventType := range eventTypes {
		wanted[eventType] = true
	}
	var ids []uuid.UUID
	for _, key := range e.store.eventOrder {
		event := e.store.events[key]
		created := e.store.created[key]
		if event == nil || event.WebhookConfigID == nil || *event.WebhookConfigID != configID || !e.store.ownedBy(key, accountID) {
			continue
		}
		if created.Before(since) || !created.Before(until) || (len(wanted) > 0 && !wanted[event.EventType]) {
			continue
		}
		ids = append(ids, event.ID)
		if len(ids) == limit {
			break
		}
	}
	return ids, nil
}

// RequeueForRedelivery mirrors the repository: the listed events of the config
// about a wallet of accountID created since createdSince go back to pending with
// no attempts, except a pending event touched after busySince.
func (e memoryEvents) RequeueForRedelivery(_ context.Context, configID, accountID uuid.UUID, ids []uuid.UUID, createdSince, busySince time.Time) ([]models.WebhookEvent, error) {
	e.store.mu.Lock()
	defer e.store.mu.Unlock()
	now := time.Now().UTC()
	var out []models.WebhookEvent
	for _, id := range ids {
		key := id.String()
		event := e.store.events[key]
		if event == nil || event.WebhookConfigID == nil || *event.WebhookConfigID != configID || !e.store.ownedBy(key, accountID) || e.store.created[key].Before(createdSince) {
			continue
		}
		touched := e.store.touched[key]
		if touched.IsZero() {
			touched = e.store.created[key]
		}
		if event.DeliveryStatus == models.WebhookDeliveryPending && touched.After(busySince) {
			continue
		}
		event.DeliveryStatus = models.WebhookDeliveryPending
		event.Attempts = 0
		event.DeliveredAt = nil
		e.store.touched[key] = now
		out = append(out, *event)
	}
	return out, nil
}

// ownEvent records that the event is about a wallet of accountID.
func (m *memoryWebhook) ownEvent(id, accountID uuid.UUID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.owners[id.String()] = accountID
}

// ownedBy reports whether the event is about a wallet of accountID. The caller holds the lock.
func (m *memoryWebhook) ownedBy(key string, accountID uuid.UUID) bool {
	owner, ok := m.owners[key]
	return ok && owner == accountID
}

// backdate moves an event's insert and last touch back by ago.
func (m *memoryWebhook) backdate(id string, ago time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	at := time.Now().UTC().Add(-ago)
	m.created[id] = at
	m.touched[id] = at
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
