package models

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

const activityMetadataMaxBytes = 4096

var (
	activityFieldName  = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	activityName       = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
	activityPermission = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)
)

// activityFlagKeys are the catalog flag names a before/after map may name.
// A value is a boolean. A secret is not a key and is not a value.
var activityFlagKeys = map[string]struct{}{
	"api-request-signature-required": {},
	"deposit-scan-enabled":           {},
	"sweep-enabled":                  {},
	"user-2fa-required":              {},
	"wallet-creation-enabled":        {},
	"webhook-delivery-enabled":       {},
	"withdrawals-enabled":            {},
}

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
		case "name":
			text, err := activityLabel(value)
			if err != nil {
				return "", fmt.Errorf("activity metadata name: %w", err)
			}
			cleaned[key] = text
		case "permissions":
			names, err := activityPermissions(value)
			if err != nil {
				return "", err
			}
			cleaned[key] = names
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
		case "before", "after":
			flags, err := activityBooleanMap(value)
			if err != nil {
				return "", fmt.Errorf("activity metadata %s: %w", key, err)
			}
			cleaned[key] = flags
		default:
			return "", fmt.Errorf("activity metadata key %q is not allowed", key)
		}
	}
	if err := activityFlagPair(cleaned); err != nil {
		return "", err
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

func activityLabel(value any) (string, error) {
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("must be a string")
	}
	text = strings.TrimSpace(text)
	if text == "" || len([]rune(text)) > 255 {
		return "", fmt.Errorf("must be a short label")
	}
	for _, r := range text {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("must be a short label")
		}
	}
	return text, nil
}

func activityPermissions(value any) ([]string, error) {
	names, ok := value.([]string)
	if !ok || len(names) == 0 {
		return nil, fmt.Errorf("activity metadata permissions are required")
	}
	out := make([]string, len(names))
	seen := map[string]struct{}{}
	for i, name := range names {
		if !activityPermission.MatchString(name) || len(name) > 64 || !activityPermissionAllowed(name) {
			return nil, fmt.Errorf("activity metadata permission %q is not allowed", name)
		}
		if _, ok := seen[name]; ok {
			return nil, fmt.Errorf("activity metadata permission %q is duplicated", name)
		}
		seen[name] = struct{}{}
		out[i] = name
	}
	slices.Sort(out)
	return out, nil
}

func activityPermissionAllowed(name string) bool {
	switch name {
	case "addresses.create", "sweep.execute", "transactions.read",
		"wallets.create", "wallets.read",
		"webhooks.read", "webhooks.write",
		"withdrawals.create":
		return true
	default:
		return false
	}
}

func activityBooleanMap(value any) (map[string]bool, error) {
	flags, ok := value.(map[string]bool)
	if !ok || len(flags) == 0 {
		return nil, fmt.Errorf("must be booleans")
	}
	out := make(map[string]bool, len(flags))
	for key, enabled := range flags {
		if _, allowed := activityFlagKeys[key]; !allowed {
			return nil, fmt.Errorf("flag is not in the catalog")
		}
		out[key] = enabled
	}
	return out, nil
}

func activityFlagPair(cleaned map[string]any) error {
	before, hasBefore := cleaned["before"]
	after, hasAfter := cleaned["after"]
	if !hasBefore && !hasAfter {
		return nil
	}
	if !hasBefore || !hasAfter {
		return fmt.Errorf("activity metadata before and after are a pair")
	}
	for key := range cleaned {
		if key != "before" && key != "after" {
			return fmt.Errorf("activity metadata key %q is not allowed beside a flag map", key)
		}
	}
	left, leftOK := before.(map[string]bool)
	right, rightOK := after.(map[string]bool)
	if !leftOK || !rightOK || len(left) == 0 || len(left) != len(right) {
		return fmt.Errorf("activity metadata before and after must be the same flags")
	}
	for key, previous := range left {
		next, ok := right[key]
		if !ok {
			return fmt.Errorf("activity metadata before and after must be the same flags")
		}
		if previous == next {
			return fmt.Errorf("activity metadata records an unchanged flag")
		}
	}
	return nil
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
