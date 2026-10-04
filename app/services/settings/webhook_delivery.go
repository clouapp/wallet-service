package settings

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	activitylog "github.com/macrowallets/waas/app/services/activity"
)

// WebhookDeliveryValues is the stored webhook_delivery row. A zero field is
// missing or invalid: delivery keeps the code default for that field.
type WebhookDeliveryValues struct {
	MaxAttempts    int
	TimeoutSeconds int
}

// platformWriter stores one platform group (account_id NULL).
type platformWriter interface {
	UpsertPlatform(ctx context.Context, group string, values map[string]string) error
}

// EffectiveWebhookDelivery reads the platform webhook_delivery group at the
// moment of use. A sealed cache hit skips the database. A cache miss, a cache
// failure, or a bad seal reads the database. A missing row leaves every field
// zero so the caller keeps the code default. One invalid key falls back to
// zero for that key only. A database failure is returned so the caller can
// keep the default and still deliver.
func (s *Service) EffectiveWebhookDelivery(ctx context.Context) (WebhookDeliveryValues, error) {
	if s == nil {
		return WebhookDeliveryValues{}, errServiceRequired
	}
	if ctx == nil {
		return WebhookDeliveryValues{}, fmt.Errorf("webhook delivery settings: context is required")
	}
	group, ok := FindGroup(groupWebhookDelivery)
	if !ok {
		return WebhookDeliveryValues{}, ErrGroupNotFound
	}
	stored, err := s.platformValues(ctx, group.Name)
	if err != nil {
		return WebhookDeliveryValues{}, err
	}
	return parseWebhookDelivery(stored), nil
}

func parseWebhookDelivery(stored map[string]string) WebhookDeliveryValues {
	return WebhookDeliveryValues{
		MaxAttempts:    webhookOverride(stored, keyMaxAttempts, minWebhookAttempts, maxWebhookAttempts),
		TimeoutSeconds: webhookOverride(stored, keyTimeoutSeconds, 1, maxWebhookTimeoutSeconds),
	}
}

// webhookOverride returns a stored integer inside the inclusive bounds.
// Missing, blank, unparsable, and out-of-range values return 0 so the code
// default stands. The key name is the only thing logged.
func webhookOverride(stored map[string]string, key string, floor, ceiling int) int {
	raw, ok := stored[key]
	if !ok {
		return 0
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || parsed < floor || parsed > ceiling {
		slog.Warn("webhook delivery setting fell back to the default", "key", key)
		return 0
	}
	return parsed
}

// SavePlatform writes one platform group. An unknown group, including an
// account group, is ErrGroupNotFound before the platform-admin check. S1.4.4
// names settings.update plus the group's own UpdatePermission. This branch
// has no platform permission catalog, and webhook_delivery names no
// permission, so a platform_admins row is the gate. A zero or negative
// attempt or timeout is a ValidationError and is not stored. The activity
// row is settings.updated and names the group and the field names, never
// the values.
func (s *Service) SavePlatform(ctx context.Context, actorID uuid.UUID, groupName string, body map[string]any) (GroupView, error) {
	if ctx == nil {
		return GroupView{}, fmt.Errorf("platform settings: context is required")
	}
	if s == nil || s.store == nil || s.activity == nil {
		return GroupView{}, errServiceRequired
	}
	groupName = strings.TrimSpace(groupName)
	group, ok := FindGroup(groupName)
	if !ok || group.Scope != ScopePlatform {
		return GroupView{}, ErrGroupNotFound
	}
	if actorID == uuid.Nil {
		return GroupView{}, fmt.Errorf("platform settings: actor is required")
	}
	if s.admins == nil {
		return GroupView{}, fmt.Errorf("platform settings: platform admins are required")
	}
	admin, err := s.admins.Contains(ctx, actorID)
	if err != nil {
		return GroupView{}, err
	}
	if !admin {
		return GroupView{}, ErrPlatformForbidden
	}
	if body == nil {
		body = map[string]any{}
	}
	stored, err := s.listPlatformValues(ctx, group.Name)
	if err != nil {
		return GroupView{}, err
	}
	writes, err := s.collectWrites(group, stored, body)
	if err != nil {
		return GroupView{}, err
	}
	if group.Name == groupPriceLookup {
		if err := s.rejectDisabledPriceProviders(ctx, stored, writes); err != nil {
			return GroupView{}, err
		}
	}
	if len(writes) == 0 {
		return s.platformGroupView(ctx, group)
	}
	writer, ok := s.store.(platformWriter)
	if !ok {
		return GroupView{}, fmt.Errorf("platform settings: store cannot write a platform group")
	}
	fields := slices.Sorted(maps.Keys(writes))
	err = s.activity.Within(ctx, func(ctx context.Context) error {
		if err := writer.UpsertPlatform(ctx, group.Name, writes); err != nil {
			return err
		}
		meta, err := activitylog.SettingsChange(group.Name, fields)
		if err != nil {
			return err
		}
		return s.activity.Append(ctx, models.AccountActivity{
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
	auditErr := s.recordSettingsAudit(ctx, nil, group, stored, writes)
	s.cache.Forget(platformCacheKey(group.Name))
	if auditErr != nil {
		return GroupView{}, auditErr
	}
	return s.platformGroupView(ctx, group)
}

func (s *Service) listPlatformValues(ctx context.Context, group string) (map[string]string, error) {
	reader, ok := s.store.(platformReader)
	if !ok {
		return nil, fmt.Errorf("platform settings: platform reader is required")
	}
	rows, err := reader.ListPlatform(ctx, group)
	if err != nil {
		return nil, err
	}
	return valuesFromRows(rows), nil
}

func (s *Service) platformGroupView(ctx context.Context, group Group) (GroupView, error) {
	reader, ok := s.store.(platformReader)
	if !ok {
		return GroupView{}, fmt.Errorf("platform settings: platform reader is required")
	}
	rows, err := reader.ListPlatform(ctx, group.Name)
	if err != nil {
		return GroupView{}, err
	}
	return renderStoredGroup(group, rows, true), nil
}
