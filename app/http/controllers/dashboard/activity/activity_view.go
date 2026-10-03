package activity

import (
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

// AccountActivityView is one activity row the dashboard reads. Field order and
// tags match the model wire. Metadata stays the model type so a nil map is
// still {} and a nil slice inside it stays null. A nil page stays nil; an
// empty page stays empty.
type AccountActivityView struct {
	ID          uuid.UUID               `json:"id"`
	AccountID   *uuid.UUID              `json:"account_id"`
	ActorUserID uuid.UUID               `json:"actor_user_id"`
	Action      string                  `json:"action"`
	TargetType  string                  `json:"target_type"`
	TargetID    string                  `json:"target_id"`
	Metadata    models.ActivityMetadata `json:"metadata"`
	CreatedAt   time.Time               `json:"created_at"`
}

func newAccountActivityView(row models.AccountActivity) AccountActivityView {
	return AccountActivityView{
		ID:          row.ID,
		AccountID:   row.AccountID,
		ActorUserID: row.ActorUserID,
		Action:      row.Action,
		TargetType:  row.TargetType,
		TargetID:    row.TargetID,
		Metadata:    row.Metadata,
		CreatedAt:   row.CreatedAt,
	}
}

func accountActivityViews(rows []models.AccountActivity) []AccountActivityView {
	if rows == nil {
		return nil
	}
	views := make([]AccountActivityView, len(rows))
	for i := range rows {
		views[i] = newAccountActivityView(rows[i])
	}
	return views
}
