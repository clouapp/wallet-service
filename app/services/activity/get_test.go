package activity

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

type getReader struct {
	findCalls int
	row       *models.AccountActivity
	err       error
}

func (r *getReader) List(context.Context, uuid.UUID, int, int) ([]models.AccountActivity, int64, error) {
	return nil, 0, errors.New("list is not used by Get")
}

func (r *getReader) ListPlatform(context.Context, int, int) ([]models.AccountActivity, int64, error) {
	return nil, 0, errors.New("platform list is not used by Get")
}

func (r *getReader) Find(context.Context, uuid.UUID, uuid.UUID) (*models.AccountActivity, error) {
	r.findCalls++
	if r.err != nil {
		return nil, r.err
	}
	return r.row, nil
}

func TestGet_Resolves_TheRowBeforeActivityRead(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	activityID := uuid.New()
	stored := accountID
	reader := &getReader{row: &models.AccountActivity{
		ID: activityID, AccountID: &stored, ActorUserID: uuid.New(), Action: "member.removed",
	}}
	service := NewService(Deps{Rows: reader})

	for _, role := range []string{"owner", "admin", "auditor"} {
		reader.findCalls = 0
		got, err := service.Get(context.Background(), accountID, role, activityID)
		if err != nil {
			t.Fatalf("Get %s: %v", role, err)
		}
		if got.ID != activityID || reader.findCalls != 1 {
			t.Fatalf("Get %s row=%s calls=%d", role, got.ID, reader.findCalls)
		}
	}

	reader.findCalls = 0
	if _, err := service.Get(context.Background(), accountID, "user", activityID); !errors.Is(err, ErrReadForbidden) || reader.findCalls != 1 {
		t.Fatalf("user err=%v calls=%d", err, reader.findCalls)
	}
	reader.findCalls = 0
	if _, err := service.Get(context.Background(), accountID, "user", uuid.Nil); !errors.Is(err, ErrNotFound) || reader.findCalls != 0 {
		t.Fatalf("user with an empty id err=%v calls=%d", err, reader.findCalls)
	}
	missing := &getReader{err: models.ErrRepositoryNotFound}
	if _, err := NewService(Deps{Rows: missing}).Get(context.Background(), accountID, "user", uuid.New()); !errors.Is(err, ErrNotFound) || missing.findCalls != 1 {
		t.Fatalf("user missing row err=%v calls=%d", err, missing.findCalls)
	}
}

func TestGet_Hides_ForeignPlatformAndUnknownRows(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	otherID := uuid.New()
	service := NewService(Deps{Rows: &getReader{err: models.ErrRepositoryNotFound}})

	for _, id := range []uuid.UUID{uuid.New(), uuid.Nil} {
		if _, err := service.Get(context.Background(), accountID, "auditor", id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing %s: %v", id, err)
		}
	}

	foreign := &getReader{row: &models.AccountActivity{ID: uuid.New(), AccountID: &otherID}}
	if _, err := NewService(Deps{Rows: foreign}).Get(context.Background(), accountID, "owner", foreign.row.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other account: %v", err)
	}

	platform := &getReader{row: &models.AccountActivity{ID: uuid.New()}}
	if _, err := NewService(Deps{Rows: platform}).Get(context.Background(), accountID, "admin", platform.row.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("platform row: %v", err)
	}
}
