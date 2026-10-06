package withdrawalrecords

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	activitylog "github.com/macrowallets/waas/app/services/activity"
)

type cancelStore struct {
	status  string
	withins int
}

func (s *cancelStore) FindByWallet(context.Context, uuid.UUID, string, int, int) ([]models.Withdrawal, int64, error) {
	return nil, 0, nil
}
func (s *cancelStore) FindByID(context.Context, uuid.UUID) (*models.Withdrawal, error) {
	return nil, nil
}
func (s *cancelStore) FindByIDAndWallet(context.Context, uuid.UUID, uuid.UUID) (*models.Withdrawal, error) {
	return nil, nil
}
func (s *cancelStore) Within(ctx context.Context, fn func(context.Context) error) error {
	s.withins++
	if fn == nil {
		return nil
	}
	return fn(ctx)
}
func (s *cancelStore) Create(context.Context, *models.Withdrawal) error { return nil }
func (s *cancelStore) RetryBroadcast(context.Context, uuid.UUID, string, string, string, string) error {
	return nil
}
func (s *cancelStore) MarkFailed(context.Context, uuid.UUID, string) error { return nil }
func (s *cancelStore) MarkBroadcast(context.Context, uuid.UUID, *uuid.UUID) error {
	return nil
}
func (s *cancelStore) SetStatus(_ context.Context, _ uuid.UUID, status string) error {
	s.status = status
	return nil
}

type cancelActivity struct {
	row models.AccountActivity
}

func (a *cancelActivity) Within(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}
func (a *cancelActivity) Append(_ context.Context, row models.AccountActivity) error {
	a.row = row
	return nil
}

func TestWithin_Opens_TheStoreTransaction(t *testing.T) {
	store := &cancelStore{}
	records := NewRecords(Deps{Store: store})
	called := false
	err := records.Within(context.Background(), func(context.Context) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("within: %v", err)
	}
	if !called || store.withins != 1 {
		t.Fatalf("called=%v withins=%d", called, store.withins)
	}
}

func TestCancel_Records_TheEventWithoutAnAmount(t *testing.T) {
	store := &cancelStore{}
	activity := &cancelActivity{}
	records := NewRecords(Deps{Store: store, Activity: activity})
	accountID := uuid.New()
	actorID := uuid.New()
	withdrawalID := uuid.New()

	err := records.Cancel(context.Background(), accountID, actorID, withdrawalID)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if store.status != "cancelled" {
		t.Fatalf("status = %s", store.status)
	}
	if activity.row.Action != activitylog.ActionWithdrawalCancelled {
		t.Fatalf("action = %s", activity.row.Action)
	}
	if activity.row.AccountID == nil || *activity.row.AccountID != accountID {
		t.Fatalf("account = %v", activity.row.AccountID)
	}
	encoded, err := activity.row.Metadata.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if strings.Contains(encoded, "amount") || strings.Contains(encoded, "-") {
		t.Fatalf("metadata stored an amount: %s", encoded)
	}
}
