package activity

import (
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

// AccountActivity is one activity row the dashboard reads. Field order and
// tags match the model wire. Metadata stays the model type so a nil map is
// still {} and a nil slice inside it stays null. A nil page stays nil; an
// empty page stays empty.
type AccountActivity struct {
	ID          uuid.UUID               `json:"id"`
	AccountID   *uuid.UUID              `json:"account_id"`
	ActorUserID uuid.UUID               `json:"actor_user_id"`
	Action      string                  `json:"action"`
	TargetType  string                  `json:"target_type"`
	TargetID    string                  `json:"target_id"`
	Metadata    models.ActivityMetadata `json:"metadata"`
	CreatedAt   time.Time               `json:"created_at"`
}

// AccountActivityFrom projects one activity row.
func AccountActivityFrom(row models.AccountActivity) AccountActivity {
	return AccountActivity{
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

// AccountActivitiesFrom copies a page. A nil slice stays nil; an empty slice stays empty.
func AccountActivitiesFrom(rows []models.AccountActivity) []AccountActivity {
	if rows == nil {
		return nil
	}
	views := make([]AccountActivity, len(rows))
	for i := range rows {
		views[i] = AccountActivityFrom(rows[i])
	}
	return views
}
