package settings

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

// PlatformAccountGroup reads one platform-managed account group.
// S1.4.6: GET /v1/platform/accounts/{accountId}/settings/{group} settings.view (platform-managed account groups).
// settings.view is not in a platform catalog, so a platform_admins row is the gate.
// An unknown name, a platform-only group, and an account-managed group are
// ErrGroupNotFound before the account lookup and before that gate.
// An unknown account is ErrAccountNotFound before the gate.
// A known group with no stored row is the registry defaults and is_set false.
// A secret is omitted (is_set only). The read writes no activity and lists
// only this account's rows.
func (s *Service) PlatformAccountGroup(ctx context.Context, actorID, accountID uuid.UUID, groupName string) (GroupView, error) {
	if ctx == nil {
		return GroupView{}, fmt.Errorf("platform settings: context is required")
	}
	if s == nil {
		return GroupView{}, errServiceRequired
	}
	group, ok := platformManagedAccountGroup(groupName)
	if !ok {
		return GroupView{}, ErrGroupNotFound
	}
	if accountID == uuid.Nil {
		return GroupView{}, ErrAccountNotFound
	}
	if s.accounts == nil {
		return GroupView{}, fmt.Errorf("platform settings: accounts are required")
	}
	exists, err := s.accounts.Exists(ctx, accountID)
	if err != nil {
		return GroupView{}, err
	}
	if !exists {
		return GroupView{}, ErrAccountNotFound
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
		return GroupView{}, ErrPlatformViewForbidden
	}
	if s.store == nil {
		return GroupView{}, errServiceRequired
	}
	rows, err := s.store.ListGroup(ctx, accountID, group.Name)
	if err != nil {
		return GroupView{}, err
	}
	return renderStoredGroup(group, rowsOwnedBy(accountID, group.Name, rows), true), nil
}

func platformManagedAccountGroup(name string) (Group, bool) {
	group, ok := FindGroup(strings.TrimSpace(name))
	if !ok || group.Scope != ScopeAccount || group.ManagedBy != ManagedByPlatform {
		return Group{}, false
	}
	return group, true
}

func rowsOwnedBy(accountID uuid.UUID, groupName string, rows []models.Setting) []models.Setting {
	owned := make([]models.Setting, 0, len(rows))
	for _, row := range rows {
		if row.AccountID == nil || *row.AccountID != accountID {
			continue
		}
		if row.Group != "" && row.Group != groupName {
			continue
		}
		owned = append(owned, row)
	}
	return owned
}

func renderStoredGroup(group Group, rows []models.Setting, canUpdate bool) GroupView {
	stored := map[string]models.Setting{}
	var updatedAt time.Time
	for _, row := range rows {
		stored[row.Key] = row
		if row.UpdatedAt.After(updatedAt) {
			updatedAt = row.UpdatedAt
		}
	}
	fields := make([]Field, 0, len(group.Settings))
	for _, definition := range group.Settings {
		row, present := stored[definition.Key]
		field := Field{
			Key:     definition.Key,
			Label:   definition.Label,
			Help:    definition.Help,
			Type:    definition.Type,
			Secret:  definition.Secret,
			Options: definition.Options,
		}
		if definition.Secret {
			field.IsSet = present && row.Value != ""
			fields = append(fields, field)
			continue
		}
		field.IsSet = present
		if present {
			field.Value = castOut(row.Value, definition)
		} else {
			field.Value = defaultValue(definition)
		}
		fields = append(fields, field)
	}
	view := GroupView{
		Name:      group.Name,
		Section:   group.SectionName(),
		Block:     group.Block,
		Scope:     group.Scope,
		ManagedBy: group.ManagedBy,
		CanUpdate: canUpdate,
		Fields:    fields,
	}
	if !updatedAt.IsZero() {
		updated := updatedAt
		view.UpdatedAt = &updated
	}
	return view
}
