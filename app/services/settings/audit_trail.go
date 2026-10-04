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

const accountAuditScopePrefix = "account:"

// settingsAuditScope is ” for a platform row and account:{uuid} otherwise.
func settingsAuditScope(accountID *uuid.UUID) string {
	if accountID == nil || *accountID == uuid.Nil {
		return ""
	}
	return accountAuditScopePrefix + accountID.String()
}

// prepareSettingsAudit stamps the row scope. A missing trail or causer records
// nothing. The request scope is not the row scope.
func (s *Service) prepareSettingsAudit(ctx context.Context, accountID *uuid.UUID) (context.Context, bool, error) {
	if s == nil {
		return ctx, false, fmt.Errorf("settings audit: service is required")
	}
	if audit.App == nil {
		return ctx, false, nil
	}
	if _, ok := audit.CauserFromContext(ctx); !ok {
		return ctx, false, nil
	}
	return audit.WithScope(ctx, settingsAuditScope(accountID)), true, nil
}

// recordSettingsAudit appends one activity_log entry per written key.
// Scope is ” for a platform row and account:{uuid} otherwise.
// A secret is recorded as valueSet. The stored text is not passed through.
// No causer, or a process that has not booted the trail, records nothing.
func (s *Service) recordSettingsAudit(ctx context.Context, accountID *uuid.UUID, group Group, before, writes map[string]string) error {
	if s == nil {
		return fmt.Errorf("settings audit: service is required")
	}
	if len(writes) == 0 {
		return nil
	}
	scoped, record, err := s.prepareSettingsAudit(ctx, accountID)
	if err != nil || !record {
		return err
	}
	ctx = scoped

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

type settingsAuditSnapshot struct {
	group Group
	rows  []models.Setting
}

func (s *Service) auditSnapshots(ctx context.Context, accountID *uuid.UUID, groups []Group) ([]settingsAuditSnapshot, error) {
	snapshots := make([]settingsAuditSnapshot, 0, len(groups))
	for _, group := range groups {
		rows, err := s.settingRows(ctx, accountID, group.Name)
		if err != nil {
			return nil, err
		}
		copied := make([]models.Setting, len(rows))
		copy(copied, rows)
		snapshots = append(snapshots, settingsAuditSnapshot{group: group, rows: copied})
	}
	return snapshots, nil
}

// recordRemovedSettings appends one activity_log entry per deleted key.
// Scope follows the row: ” on the platform, account:{uuid} otherwise.
// A secret contributes valueSet only.
func (s *Service) recordRemovedSettings(ctx context.Context, accountID *uuid.UUID, snapshots []settingsAuditSnapshot) error {
	for _, snapshot := range snapshots {
		if len(snapshot.rows) == 0 {
			continue
		}
		scoped, record, err := s.prepareSettingsAudit(ctx, accountID)
		if err != nil || !record {
			return err
		}
		if err := s.recordRemovedGroup(scoped, accountID, snapshot); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) recordRemovedGroup(ctx context.Context, accountID *uuid.UUID, snapshot settingsAuditSnapshot) error {
	before := valuesFromRows(snapshot.rows)
	byKey := make(map[string]models.Setting, len(snapshot.rows))
	for _, row := range snapshot.rows {
		byKey[row.Key] = row
	}
	recorder := audit.ActivityLog{}
	for _, key := range slices.Sorted(maps.Keys(before)) {
		definition, ok := Find(snapshot.group.Name, key)
		if !ok {
			return fmt.Errorf("settings audit: %s.%s is not in the registry", snapshot.group.Name, key)
		}
		row, ok := byKey[key]
		if !ok || row.ID == 0 {
			return fmt.Errorf("settings audit: %s.%s has no row id", snapshot.group.Name, key)
		}
		oldImage, _ := settingsAuditImages(accountID, snapshot.group.Name, definition, before, "")
		if err := recorder.Record(ctx, audit.RecordInput{
			Table:     "settings",
			Event:     "deleted",
			SubjectID: strconv.FormatUint(row.ID, 10),
			Old:       oldImage,
		}); err != nil {
			return err
		}
	}
	return nil
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
