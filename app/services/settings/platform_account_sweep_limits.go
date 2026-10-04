package settings

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	activitylog "github.com/macrowallets/waas/app/services/activity"
)

// AuthorizePlatformAccountSweepWrite is the gate for
// PUT /v1/platform/accounts/{accountId}/settings/{group}
// settings.update + sweep.update for account_sweep_limits.
// Those names are not in a platform catalog, so a platform_admins row is the
// gate. Any other group name is ErrGroupNotFound before the account lookup
// and before that gate. An unknown account is ErrAccountNotFound before the
// gate. The check does not read the body, the stored row, or another account.
func (s *Service) AuthorizePlatformAccountSweepWrite(ctx context.Context, actorID, accountID uuid.UUID, groupName string) error {
	_, err := s.authorizePlatformAccountSweepWrite(ctx, actorID, accountID, groupName)
	return err
}

// SavePlatformAccountSweepLimits writes the account_sweep_limits row for one
// account. S1.4.6: PUT /v1/platform/accounts/{accountId}/settings/{group}
// settings.update + sweep.update for account_sweep_limits.
// Any other group name is ErrGroupNotFound before the account lookup and
// before the platform-admin gate. An unknown account is ErrAccountNotFound
// before that gate. Counts must be positive integers. A zero or negative
// count, and a negative daily cap, are a ValidationError and are not stored.
// A blank daily_withdraw_cap_usd is stored empty, which stays unlimited.
// The activity row is settings.updated on this account and names the group
// and the field names, never the values. The account cache key for the group
// is forgotten. The platform sweep_limits row is left in place, so a later
// LoadLimits still prefers this account row.
func (s *Service) SavePlatformAccountSweepLimits(ctx context.Context, actorID, accountID uuid.UUID, groupName string, body map[string]any) (GroupView, error) {
	group, err := s.authorizePlatformAccountSweepWrite(ctx, actorID, accountID, groupName)
	if err != nil {
		return GroupView{}, err
	}
	if s.store == nil || s.activity == nil {
		return GroupView{}, errServiceRequired
	}
	if body == nil {
		body = map[string]any{}
	}
	stored, err := s.listAccountValues(ctx, accountID, group.Name)
	if err != nil {
		return GroupView{}, err
	}
	writes, err := s.collectWrites(group, stored, body)
	if err != nil {
		return GroupView{}, err
	}
	if len(writes) == 0 {
		return s.accountSweepLimitsView(ctx, accountID, group)
	}
	fields := slices.Sorted(maps.Keys(writes))
	err = s.activity.Within(ctx, func(ctx context.Context) error {
		if err := s.store.UpsertMany(ctx, accountID, group.Name, writes); err != nil {
			return err
		}
		meta, err := activitylog.SettingsChange(group.Name, fields)
		if err != nil {
			return err
		}
		id := accountID
		return s.activity.Append(ctx, models.AccountActivity{
			AccountID:   &id,
			ActorUserID: actorID,
			Action:      activitylog.ActionSettingsUpdated,
			TargetType:  activitylog.TargetSettings,
			TargetID:    group.Name,
			Metadata:    meta,
		})
	})
	if err != nil {
		return GroupView{}, err
	}
	if s.cache != nil {
		s.cache.Forget(cacheKey(accountID, group.Name))
	}
	return s.accountSweepLimitsView(ctx, accountID, group)
}

func (s *Service) authorizePlatformAccountSweepWrite(ctx context.Context, actorID, accountID uuid.UUID, groupName string) (Group, error) {
	if ctx == nil {
		return Group{}, fmt.Errorf("platform settings: context is required")
	}
	if s == nil {
		return Group{}, errServiceRequired
	}
	group, ok := accountSweepLimitsWriteGroup(groupName)
	if !ok {
		return Group{}, ErrGroupNotFound
	}
	if accountID == uuid.Nil {
		return Group{}, ErrAccountNotFound
	}
	if s.accounts == nil {
		return Group{}, fmt.Errorf("platform settings: accounts are required")
	}
	exists, err := s.accounts.Exists(ctx, accountID)
	if err != nil {
		return Group{}, err
	}
	if !exists {
		return Group{}, ErrAccountNotFound
	}
	if actorID == uuid.Nil {
		return Group{}, fmt.Errorf("platform settings: actor is required")
	}
	if s.admins == nil {
		return Group{}, fmt.Errorf("platform settings: platform admins are required")
	}
	admin, err := s.admins.Contains(ctx, actorID)
	if err != nil {
		return Group{}, err
	}
	if !admin {
		return Group{}, ErrPlatformForbidden
	}
	return group, nil
}

func accountSweepLimitsWriteGroup(name string) (Group, bool) {
	group, ok := FindGroup(strings.TrimSpace(name))
	if !ok || group.Name != groupAccountSweepLimits {
		return Group{}, false
	}
	return group, true
}

func (s *Service) accountSweepLimitsView(ctx context.Context, accountID uuid.UUID, group Group) (GroupView, error) {
	rows, err := s.store.ListGroup(ctx, accountID, group.Name)
	if err != nil {
		return GroupView{}, err
	}
	return renderStoredGroup(group, rowsOwnedBy(accountID, group.Name, rows), true), nil
}
