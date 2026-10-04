package activity

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/policies"
)

const (
	ActionMemberRoleChanged    = "member.role_changed"
	ActionMemberSuspended      = "member.suspended"
	ActionMemberReactivated    = "member.reactivated"
	ActionMemberInvited        = "member.invited"
	ActionInviteAccepted       = "invite.accepted"
	ActionSettingsUpdated      = "settings.updated"
	ActionSettingsSectionReset = "settings.section_reset"
	ActionFeaturesUpdated      = "features.updated"
	ActionChainsUpdated        = "chains.updated"
	ActionTokenCreated         = "token.created"
	ActionTokenRevoked         = "token.revoked"
	ActionMemberRemoved        = "member.removed"
	ActionUserMFAReset         = "user.mfa_reset"
	ActionUserSessionsRevoked  = "user.sessions_revoked"
	ActionUserSuspended        = "user.suspended"
	ActionUserReactivated      = "user.reactivated"
	ActionWithdrawalCancelled  = "withdrawal.cancelled"

	TargetAccountUser   = "account_user"
	TargetAccountInvite = "account_invite"
	TargetSettings      = "settings"
	TargetFeature       = "feature"
	TargetChain         = "chain"
	TargetAccessToken   = "access_token"
	TargetUser          = "user"
	TargetWithdrawal    = "withdrawal"
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

// TokenCreated records a minted API token. The metadata is the name and the
// catalog permissions. The plaintext secret, token_hash and spending limit
// are not accepted.
func TokenCreated(name, storedPermissions string) (models.ActivityMetadata, error) {
	return tokenAudit(name, storedPermissions, true)
}

// TokenRevoked records a soft revoke. A stored permission list that is not
// the catalog is omitted rather than copied, so a legacy value cannot land
// in the trail. The name is still required.
func TokenRevoked(name, storedPermissions string) (models.ActivityMetadata, error) {
	return tokenAudit(name, storedPermissions, false)
}

func tokenAudit(name, storedPermissions string, strict bool) (models.ActivityMetadata, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("activity: token name is required")
	}
	permissions, err := tokenPermissionNames(storedPermissions, strict)
	if err != nil {
		return nil, err
	}
	meta := models.ActivityMetadata{"name": name}
	if len(permissions) > 0 {
		meta["permissions"] = permissions
	}
	if _, err := meta.Encode(); err != nil {
		return nil, err
	}
	return meta, nil
}

func tokenPermissionNames(stored string, strict bool) ([]string, error) {
	stored = strings.TrimSpace(stored)
	if stored == "" {
		return nil, nil
	}
	var names []string
	if err := json.Unmarshal([]byte(stored), &names); err != nil {
		if strict {
			return nil, fmt.Errorf("activity: token permissions are not a list")
		}
		return nil, nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if !policies.IsAPITokenPermission(name) {
			if strict {
				return nil, fmt.Errorf("activity: token permission %q is not allowed", name)
			}
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	slices.Sort(out)
	return out, nil
}

// MemberRemoved records the role the membership had. It does not accept a
// token, a secret, or an amount.
func MemberRemoved(role string) (models.ActivityMetadata, error) {
	meta := models.ActivityMetadata{"role": strings.TrimSpace(role)}
	if _, err := meta.Encode(); err != nil {
		return nil, err
	}
	return meta, nil
}

// SessionsRevoked records that the user's sessions were voided. The watermark
// and any token hash are not accepted.
func SessionsRevoked() (models.ActivityMetadata, error) {
	meta := models.ActivityMetadata{"key": "sessions"}
	if _, err := meta.Encode(); err != nil {
		return nil, err
	}
	return meta, nil
}

// MemberInvited records the role offered on an invite. The raw token, its
// hash, and the address are not accepted.
func MemberInvited(role string) (models.ActivityMetadata, error) {
	return inviteRole(role)
}

// InviteAccepted records the role the invite granted. The raw token and its
// hash are not accepted.
func InviteAccepted(role string) (models.ActivityMetadata, error) {
	return inviteRole(role)
}

func inviteRole(role string) (models.ActivityMetadata, error) {
	meta := models.ActivityMetadata{"role": strings.TrimSpace(role)}
	if _, err := meta.Encode(); err != nil {
		return nil, err
	}
	return meta, nil
}

// UserSuspended records a platform suspension. The reason, the watermark
// and any secret are not accepted. member.suspended is a membership change.
func UserSuspended() (models.ActivityMetadata, error) {
	return userSuspension("suspended")
}

// UserReactivated records that a platform suspension was cleared. A secret
// is not accepted.
func UserReactivated() (models.ActivityMetadata, error) {
	return userSuspension("reactivated")
}

func userSuspension(key string) (models.ActivityMetadata, error) {
	meta := models.ActivityMetadata{"key": key}
	if _, err := meta.Encode(); err != nil {
		return nil, err
	}
	return meta, nil
}

// MFAReset records that TOTP was turned off. The secret is not accepted.
func MFAReset() (models.ActivityMetadata, error) {
	meta := models.ActivityMetadata{
		"key":     "totp",
		"enabled": false,
	}
	if _, err := meta.Encode(); err != nil {
		return nil, err
	}
	return meta, nil
}

// WithdrawalCancelled records the cancellation. The amount is not accepted.
func WithdrawalCancelled() (models.ActivityMetadata, error) {
	meta := models.ActivityMetadata{"key": "cancelled"}
	if _, err := meta.Encode(); err != nil {
		return nil, err
	}
	return meta, nil
}

// ChainThresholdsChange records the chain id and the threshold field names
// that were written. Amounts are not accepted. S1.4.4 says the edit is
// audited and does not name an event, so the action is chains.updated.
func ChainThresholdsChange(chainID string, fields []string) (models.ActivityMetadata, error) {
	chainID = strings.TrimSpace(chainID)
	if chainID == "" {
		return nil, fmt.Errorf("activity: chain id is required")
	}
	if len(fields) == 0 {
		return nil, fmt.Errorf("activity: chain fields are required")
	}
	names := append([]string(nil), fields...)
	slices.Sort(names)
	meta := models.ActivityMetadata{
		"key":    chainID,
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
