package ramps

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/macrowallets/waas/app/services/webhook"
	emodels "github.com/macrowallets/waas/packages/rampengine/app/models"
	erepo "github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/app/services/broadcast"
	"github.com/macrowallets/waas/pkg/types"
)

type eventSink struct {
	broadcast.Broadcaster
	customers CustomerStore
	holders   erepo.CustomerRepository
	kycs      erepo.CustomerProviderKycRepository
	state     StateStore
}

func newEventSink(customers CustomerStore, state StateStore, holders erepo.CustomerRepository, kycs erepo.CustomerProviderKycRepository) *eventSink {
	return &eventSink{Broadcaster: broadcast.NewMemoryBroadcaster(32), customers: customers, state: state, holders: holders, kycs: kycs}
}
func (s *eventSink) Publish(userID uint, event broadcast.TransactionEvent) {
	s.Broadcaster.Publish(userID, event)
	s.emit(event.CustomerID, "ramp.order.updated", event)
}
func (s *eventSink) PublishKyc(userID uint, event broadcast.KycEvent) {
	s.Broadcaster.PublishKyc(userID, event)
	s.emit(event.CustomerID, "customer.compliance.updated", event)
}
func (s *eventSink) TransactionUpdated(tx *emodels.Transaction) {
	switch tx.Status {
	case emodels.TxStatusCompleted:
		s.emit(tx.CustomerID, "ramp.order.completed", broadcast.NewTransactionEvent(tx))
	case emodels.TxStatusFailed:
		s.emit(tx.CustomerID, "ramp.order.failed", broadcast.NewTransactionEvent(tx))
	}
}
func (s *eventSink) KycUpdated(_ string, row *emodels.CustomerProviderKyc) {
	if row.KycStatus == emodels.KycStatusApproved {
		if _, err := s.ActivateIfApproved(row.CustomerID); err != nil {
			slog.Error("ramp business activation failed", "customer", row.CustomerID)
		}
	}
}
func (s *eventSink) ActivateIfApproved(id uint) (bool, error) {
	holder, err := s.holders.FindByID(id)
	if err != nil || holder == nil {
		return false, err
	}
	if !holder.IsBusiness() || holder.Status != emodels.CustomerStatusDraft {
		return false, nil
	}
	rows, err := s.kycs.ListByCustomer(id)
	if err != nil {
		return false, err
	}
	for _, row := range rows {
		if row.KycStatus == emodels.KycStatusApproved {
			return s.holders.ActivateBusiness(id)
		}
	}
	return false, nil
}
func (s *eventSink) emit(engineID uint, kind string, payload any) {
	ctx := context.Background()
	customer, err := s.customers.FindByEngine(ctx, engineID)
	if err != nil {
		return
	}
	// Project through the public event type; raw provider payloads and
	// photos have no path into this tenant callback.
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	var data map[string]any
	if json.Unmarshal(raw, &data) != nil {
		return
	}
	data["customer_id"] = customer.ID.String()
	data["external_id"] = customer.ExternalID
	data["schema_version"] = 1
	raw, err = json.Marshal(data)
	if err != nil {
		return
	}
	id := uuid.NewSHA1(uuid.NameSpaceOID, append([]byte(kind+":"+customer.ID.String()+":"), raw...))
	if err = s.state.AppendOutbox(ctx, id, customer.AccountID, customer.ID, kind, string(raw)); err != nil {
		slog.Error("ramp outbox insert failed", "customer_id", customer.ID, "event_type", kind)
	}
}

func (s *Service) PublishOutbox(ctx context.Context, sender *webhook.Service, limit int) (int, error) {
	rows, err := s.d.State.ListOutbox(ctx, limit)
	if err != nil {
		return 0, err
	}
	done := 0
	for _, row := range rows {
		var body any
		if err := json.Unmarshal([]byte(row.Payload), &body); err != nil {
			return done, fmt.Errorf("invalid ramp outbox payload")
		}
		if _, err := sender.EnqueueScoped(ctx, webhook.ScopedEvent{EventType: types.EventType(row.EventType), SubjectID: row.ID.String(), AccountID: &row.AccountID, Data: body, OccurredAt: row.CreatedAt}); err != nil {
			return done, err
		}
		if err := s.d.State.DeliverOutbox(ctx, row.ID); err != nil {
			return done, err
		}
		done++
	}
	return done, nil
}
