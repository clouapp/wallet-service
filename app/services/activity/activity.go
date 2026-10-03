package activity

import (
	"fmt"
	"slices"
	"strings"

	"github.com/macrowallets/waas/app/models"
)

const (
	ActionMemberRoleChanged = "member.role_changed"
	ActionMemberSuspended   = "member.suspended"
	ActionMemberReactivated = "member.reactivated"
	ActionSettingsUpdated   = "settings.updated"
	ActionFeaturesUpdated   = "features.updated"

	TargetAccountUser = "account_user"
	TargetSettings    = "settings"
	TargetFeature     = "feature"
)

// MemberChange names one membership PATCH. A status change names the row;
// a role-only change is member.role_changed. Metadata holds the new values
// that were written, never a token or secret.
func MemberChange(role, status *string) (string, models.ActivityMetadata, error) {
	if role == nil && status == nil {
		return "", nil, fmt.Errorf("activity: member change is empty")
	}
	meta := models.ActivityMetadata{}
	if role != nil {
		meta["role"] = strings.TrimSpace(*role)
	}
	action := ActionMemberRoleChanged
	if status != nil {
		meta["status"] = strings.TrimSpace(*status)
		switch meta["status"] {
		case models.MembershipStatusSuspended:
			action = ActionMemberSuspended
		case models.MembershipStatusActive:
			action = ActionMemberReactivated
		default:
			return "", nil, fmt.Errorf("activity: member status is not allowed")
		}
	}
	if _, err := meta.Encode(); err != nil {
		return "", nil, err
	}
	return action, meta, nil
}

// SettingsChange records the group and the field names that were written.
// Values, including sealed secrets, are not accepted.
func SettingsChange(group string, fields []string) (models.ActivityMetadata, error) {
	group = strings.TrimSpace(group)
	if group == "" {
		return nil, fmt.Errorf("activity: settings group is required")
	}
	if len(fields) == 0 {
		return nil, fmt.Errorf("activity: settings fields are required")
	}
	names := append([]string(nil), fields...)
	slices.Sort(names)
	meta := models.ActivityMetadata{
		"group":  group,
		"fields": names,
	}
	if _, err := meta.Encode(); err != nil {
		return nil, err
	}
	return meta, nil
}

// FeatureChange records the flag key and the boolean that was stored.
func FeatureChange(key string, enabled bool) (models.ActivityMetadata, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, fmt.Errorf("activity: feature key is required")
	}
	meta := models.ActivityMetadata{
		"key":     key,
		"enabled": enabled,
	}
	if _, err := meta.Encode(); err != nil {
		return nil, err
	}
	return meta, nil
}
