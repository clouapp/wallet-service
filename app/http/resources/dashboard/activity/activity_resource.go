package activity

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

// Metadata is the activity object on the wire. A nil map encodes as an empty
// object. A nil slice stored under a key stays null. An empty map stays {}.
type Metadata map[string]any

// MarshalJSON encodes a nil map as an empty object, matching the stored bytes.
func (m Metadata) MarshalJSON() ([]byte, error) {
	if m == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(map[string]any(m))
}

// AccountActivity is one activity row the dashboard reads. Field order and
// tags match the model wire. A nil page stays nil; an empty page stays empty.
type AccountActivity struct {
	ID          uuid.UUID  `json:"id"`
	AccountID   *uuid.UUID `json:"account_id"`
	ActorUserID uuid.UUID  `json:"actor_user_id"`
	Action      string     `json:"action"`
	TargetType  string     `json:"target_type"`
	TargetID    string     `json:"target_id"`
	Metadata    Metadata   `json:"metadata"`
	CreatedAt   time.Time  `json:"created_at"`
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
		Metadata:    Metadata(row.Metadata),
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
