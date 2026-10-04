package settings

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	audit "github.com/macrowallets/waas/packages/activitylog"
)

// recordSettingsAudit appends one activity_log entry per written key.
// A secret is recorded as valueSet. The stored text is not passed through.
// No causer, or a process that has not booted the trail, records nothing.
func (s *Service) recordSettingsAudit(ctx context.Context, accountID *uuid.UUID, group Group, before, writes map[string]string) error {
	if s == nil {
		return fmt.Errorf("settings audit: service is required")
	}
	if audit.App == nil {
		return nil
	}
	if _, ok := audit.CauserFromContext(ctx); !ok {
		return nil
	}
	if len(writes) == 0 {
		return nil
	}

	rows, err := s.settingRows(ctx, accountID, group.Name)
	if err != nil {
		return err
	}
	byKey := make(map[string]models.Setting, len(rows))
	for _, row := range rows {
		byKey[row.Key] = row
	}

	recorder := audit.ActivityLog{}
	for _, key := range slices.Sorted(maps.Keys(writes)) {
		definition, ok := Find(group.Name, key)
		if !ok {
			return fmt.Errorf("settings audit: %s.%s is not in the registry", group.Name, key)
		}
		row, ok := byKey[key]
		if !ok || row.ID == 0 {
			return fmt.Errorf("settings audit: %s.%s has no row id", group.Name, key)
		}
		oldImage, newImage := settingsAuditImages(accountID, group.Name, definition, before, writes[key])
		event := "updated"
		if _, existed := before[key]; !existed {
			event = "created"
			oldImage = nil
		}
		if err := recorder.Record(ctx, audit.RecordInput{
			Table:     "settings",
			Event:     event,
			SubjectID: strconv.FormatUint(row.ID, 10),
			Old:       oldImage,
			New:       newImage,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) settingRows(ctx context.Context, accountID *uuid.UUID, group string) ([]models.Setting, error) {
	if accountID == nil {
		reader, ok := s.store.(platformReader)
		if !ok {
			return nil, fmt.Errorf("settings audit: platform reader is required")
		}
		return reader.ListPlatform(ctx, group)
	}
	if s.store == nil {
		return nil, fmt.Errorf("settings audit: store is required")
	}
	return s.store.ListGroup(ctx, *accountID, group)
}

// settingsAuditImages builds the before/after maps for one key. A secret
// contributes valueSet only. The ciphertext arguments are never copied.
func settingsAuditImages(accountID *uuid.UUID, group string, definition Definition, before map[string]string, written string) (map[string]any, map[string]any) {
	oldImage := map[string]any{"group": group, "key": definition.Key}
	newImage := map[string]any{"group": group, "key": definition.Key}
	if accountID != nil && *accountID != uuid.Nil {
		oldImage["account_id"] = accountID.String()
		newImage["account_id"] = accountID.String()
	}
	if definition.Secret {
		previous, existed := before[definition.Key]
		oldImage["valueSet"] = existed && strings.TrimSpace(previous) != ""
		newImage["valueSet"] = strings.TrimSpace(written) != ""
		return oldImage, newImage
	}
	if previous, existed := before[definition.Key]; existed {
		oldImage["value"] = previous
	}
	newImage["value"] = written
	return oldImage, newImage
}
