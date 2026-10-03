package models

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
)

const activityMetadataMaxBytes = 4096

var (
	activityFieldName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	activityName      = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
)

// ActivityMetadata is the JSON object stored with one activity row.
// Encode accepts only the names and booleans the writers are allowed to keep.
type ActivityMetadata map[string]any

// AccountActivity is one audit row. A nil AccountID is a platform write.
// Metadata never holds a secret value.
type AccountActivity struct {
	ID          uuid.UUID        `gorm:"column:id;type:uuid;primaryKey"`
	AccountID   *uuid.UUID       `gorm:"column:account_id"`
	ActorUserID uuid.UUID        `gorm:"column:actor_user_id"`
	Action      string           `gorm:"column:action"`
	TargetType  string           `gorm:"column:target_type"`
	TargetID    string           `gorm:"column:target_id"`
	Metadata    ActivityMetadata `gorm:"column:metadata;type:jsonb"`
	CreatedAt   time.Time        `gorm:"column:created_at"`
}

func (AccountActivity) TableName() string { return "account_activity" }

// Encode checks the allowlist and returns the JSON object to store.
func (m ActivityMetadata) Encode() (string, error) {
	if len(m) == 0 {
		return "", fmt.Errorf("activity metadata is empty")
	}
	cleaned := make(map[string]any, len(m))
	for key, value := range m {
		switch key {
		case "role":
			text, err := activityToken(value, "owner", "admin", "auditor", "user")
			if err != nil {
				return "", fmt.Errorf("activity metadata role: %w", err)
			}
			cleaned[key] = text
		case "status":
			text, err := activityToken(value, MembershipStatusActive, MembershipStatusSuspended)
			if err != nil {
				return "", fmt.Errorf("activity metadata status: %w", err)
			}
			cleaned[key] = text
		case "group", "key":
			text, err := activityNameValue(value)
			if err != nil {
				return "", fmt.Errorf("activity metadata %s: %w", key, err)
			}
			cleaned[key] = text
		case "fields":
			names, err := activityFields(value)
			if err != nil {
				return "", err
			}
			cleaned[key] = names
		case "enabled":
			flag, ok := value.(bool)
			if !ok {
				return "", fmt.Errorf("activity metadata enabled must be a boolean")
			}
			cleaned[key] = flag
		default:
			return "", fmt.Errorf("activity metadata key %q is not allowed", key)
		}
	}
	encoded, err := json.Marshal(cleaned)
	if err != nil {
		return "", fmt.Errorf("activity metadata: %w", err)
	}
	if len(encoded) > activityMetadataMaxBytes {
		return "", fmt.Errorf("activity metadata is too large")
	}
	return string(encoded), nil
}

func (m ActivityMetadata) Value() (driver.Value, error) {
	if m == nil {
		return "{}", nil
	}
	encoded, err := json.Marshal(map[string]any(m))
	if err != nil {
		return nil, err
	}
	return string(encoded), nil
}

func (m ActivityMetadata) MarshalJSON() ([]byte, error) {
	if m == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(map[string]any(m))
}

func (m *ActivityMetadata) Scan(src any) error {
	if m == nil {
		return fmt.Errorf("activity metadata scan: destination is nil")
	}
	if src == nil {
		*m = ActivityMetadata{}
		return nil
	}
	var data []byte
	switch value := src.(type) {
	case []byte:
		data = value
	case string:
		data = []byte(value)
	default:
		return fmt.Errorf("activity metadata scan: unsupported type %T", src)
	}
	if len(data) == 0 {
		*m = ActivityMetadata{}
		return nil
	}
	decoded := ActivityMetadata{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return fmt.Errorf("activity metadata scan: %w", err)
	}
	*m = decoded
	return nil
}

func activityToken(value any, allowed ...string) (string, error) {
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("must be a string")
	}
	for _, candidate := range allowed {
		if text == candidate {
			return text, nil
		}
	}
	return "", fmt.Errorf("%q is not allowed", text)
}

func activityNameValue(value any) (string, error) {
	text, ok := value.(string)
	if !ok || !activityName.MatchString(text) || len(text) > 64 {
		return "", fmt.Errorf("must be a short name")
	}
	return text, nil
}

func activityFields(value any) ([]string, error) {
	switch names := value.(type) {
	case []string:
		if len(names) == 0 {
			return nil, fmt.Errorf("activity metadata fields are required")
		}
		out := make([]string, len(names))
		for i, name := range names {
			if !activityFieldName.MatchString(name) || len(name) > 64 {
				return nil, fmt.Errorf("activity metadata field %q is not a name", name)
			}
			out[i] = name
		}
		return out, nil
	default:
		return nil, fmt.Errorf("activity metadata fields must be names")
	}
}
