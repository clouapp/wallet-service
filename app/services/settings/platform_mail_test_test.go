package settings

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

func TestAuthorizePlatformMailTestAllowsAnAdminAndWritesNothing(t *testing.T) {
	t.Parallel()

	actor := uuid.New()
	activity := &countingMailTestActivity{}
	store := newMemoryStore()
	service := NewService(Deps{Store: store, Sealer: prefixSealer{}, Cache: nopCache{}, Activity: activity}).
		WithPlatformAdmins(allowPlatformAdmins{ids: map[uuid.UUID]bool{actor: true}})

	if err := service.AuthorizePlatformMailTest(context.Background(), actor); err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if activity.n != 0 {
		t.Fatalf("activity rows = %d", activity.n)
	}
	if len(store.rows) != 0 {
		t.Fatalf("settings rows = %d", len(store.rows))
	}
}

func TestAuthorizePlatformMailTestRefusesANonAdmin(t *testing.T) {
	t.Parallel()

	activity := &countingMailTestActivity{}
	service := NewService(Deps{Store: newMemoryStore(), Sealer: prefixSealer{}, Cache: nopCache{}, Activity: activity}).
		WithPlatformAdmins(allowPlatformAdmins{})

	err := service.AuthorizePlatformMailTest(context.Background(), uuid.New())
	if !errors.Is(err, ErrPlatformForbidden) {
		t.Fatalf("err = %v", err)
	}
	if activity.n != 0 {
		t.Fatalf("activity rows = %d", activity.n)
	}
}

type countingMailTestActivity struct {
	n int
}

func (c *countingMailTestActivity) Within(ctx context.Context, fn func(context.Context) error) error {
	if fn == nil {
		return errors.New("activity callback is required")
	}
	return fn(ctx)
}

func (c *countingMailTestActivity) Append(context.Context, models.AccountActivity) error {
	c.n++
	return nil
}
