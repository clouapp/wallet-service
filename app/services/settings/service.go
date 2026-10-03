package settings

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/policies"
	activitylog "github.com/macrowallets/waas/app/services/activity"
)

const cacheKeyPrefix = "settings:account:"

// Store reads and writes one account group's values.
type Store interface {
	ListGroup(ctx context.Context, accountID uuid.UUID, group string) ([]models.Setting, error)
	UpsertMany(ctx context.Context, accountID uuid.UUID, group string, values map[string]string) error
}

// Sealer encrypts a secret before it is stored. It never logs the plaintext.
type Sealer interface {
	Seal(plaintext string) (string, error)
}

// Cache forgets one group after a write. A cache miss only costs a query.
type Cache interface {
	Forget(key string) bool
}

// Service reads and writes the account settings registry.
type Service struct {
	store    Store
	sealer   Sealer
	cache    Cache
	activity activitylog.Writer
}

// NewService builds the account settings service.
func NewService(store Store, sealer Sealer, cache Cache, activity activitylog.Writer) *Service {
	if store == nil {
		panic("account settings service: store is required")
	}
	if sealer == nil {
		panic("account settings service: sealer is required")
	}
	if activity == nil {
		panic("account settings service: activity log is required")
	}
	if cache == nil {
		cache = nopCache{}
	}
	return &Service{store: store, sealer: sealer, cache: cache, activity: activity}
}

// FacadeCache forgets keys through the process cache.
type FacadeCache struct{}

// Forget drops one cache key. A missing cache is a no-op.
func (FacadeCache) Forget(key string) bool {
	cache := facades.Cache()
	if cache == nil {
		return false
	}
	return cache.Forget(key)
}

type nopCache struct{}

func (nopCache) Forget(string) bool { return false }

// Registry returns every account group the role may read, with secrets replaced
// by is_set.
func (s *Service) Registry(ctx context.Context, accountID uuid.UUID, role string) (RegistryView, error) {
	if err := requireAccount(ctx, accountID); err != nil {
		return RegistryView{}, err
	}
	if !policies.MayViewSettings(role) {
		return RegistryView{}, ErrViewForbidden
	}

	view := RegistryView{
		Permissions: Permissions{View: policies.PermSettingsView, Update: policies.PermSettingsUpdate},
		Sections:    []SectionView{},
	}
	sectionAt := map[string]int{}
	blockAt := map[string]map[string]int{}
	for _, group := range Registry() {
		if group.Scope != ScopeAccount {
			continue
		}
		rendered, err := s.groupView(ctx, accountID, role, group)
		if err != nil {
			return RegistryView{}, err
		}
		sectionName := group.SectionName()
		sectionIndex, ok := sectionAt[sectionName]
		if !ok {
			sectionIndex = len(view.Sections)
			sectionAt[sectionName] = sectionIndex
			view.Sections = append(view.Sections, SectionView{Name: sectionName, Blocks: []BlockView{}})
			blockAt[sectionName] = map[string]int{}
		}
		blockIndex, ok := blockAt[sectionName][group.Block]
		if !ok {
			blockIndex = len(view.Sections[sectionIndex].Blocks)
			blockAt[sectionName][group.Block] = blockIndex
			view.Sections[sectionIndex].Blocks = append(view.Sections[sectionIndex].Blocks, BlockView{
				Title:  group.Block,
				Groups: []GroupView{},
			})
		}
		view.Sections[sectionIndex].Blocks[blockIndex].Groups = append(
			view.Sections[sectionIndex].Blocks[blockIndex].Groups,
			rendered,
		)
	}
	return view, nil
}

// Save writes one group. An unknown group is ErrGroupNotFound before the
// permission check. A blank or omitted secret keeps the stored ciphertext.
// A real write and its activity row commit together. Metadata stores the
// group and the field names, never the values.
func (s *Service) Save(ctx context.Context, accountID, actorID uuid.UUID, role, groupName string, body map[string]any) (GroupView, error) {
	if err := requireAccount(ctx, accountID); err != nil {
		return GroupView{}, err
	}
	groupName = strings.TrimSpace(groupName)
	group, ok := FindGroup(groupName)
	if !ok || group.Scope != ScopeAccount {
		return GroupView{}, ErrGroupNotFound
	}
	if !policies.MayUpdateSettings(role) {
		return GroupView{}, ErrUpdateForbidden
	}
	if group.ManagedBy != ManagedByAccount {
		return GroupView{}, ErrManagedByPlatform
	}
	if body == nil {
		body = map[string]any{}
	}

	stored, err := s.storedValues(ctx, accountID, group.Name)
	if err != nil {
		return GroupView{}, err
	}

	writes := map[string]string{}
	invalid := &ValidationError{}
	for _, key := range slices.Sorted(maps.Keys(body)) {
		definition, known := Find(group.Name, key)
		if !known {
			invalid.add(key, "unknown settings key")
			continue
		}
		value, skip, prepareErr := s.prepareValue(definition, body[key])
		if errors.Is(prepareErr, errSealFailed) {
			return GroupView{}, prepareErr
		}
		if prepareErr != nil {
			invalid.add(key, prepareErr.Error())
			continue
		}
		if skip {
			continue
		}
		writes[key] = value
	}
	if !invalid.empty() {
		return GroupView{}, invalid
	}
	if group.Validate != nil {
		if validateErr := group.Validate(effectiveNonSecrets(group.Settings, stored, writes)); validateErr != nil {
			return GroupView{}, validateErr
		}
	}
	if len(writes) == 0 {
		return s.groupView(ctx, accountID, role, group)
	}
	if actorID == uuid.Nil {
		return GroupView{}, fmt.Errorf("account settings: actor is required")
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
	s.cache.Forget(cacheKey(accountID, group.Name))
	return s.groupView(ctx, accountID, role, group)
}

// groupDeleter removes the stored rows of one account group. The settings
// service store is not required to delete until a section is reset.
type groupDeleter interface {
	DeleteGroup(ctx context.Context, accountID uuid.UUID, group string) error
}

// ResetSection deletes the stored rows of every account-managed group on one
// page, so the next read uses registry defaults. An unknown page, including a
// platform-only page, is ErrSectionNotFound before the role check. A page that
// holds a platform-managed group is ErrManagedByPlatform and is not changed.
// The activity rows name each group and its field names, never the values.
func (s *Service) ResetSection(ctx context.Context, accountID, actorID uuid.UUID, role, section string) (SectionView, error) {
	if err := requireAccount(ctx, accountID); err != nil {
		return SectionView{}, err
	}
	groups := accountGroupsInSection(section)
	if len(groups) == 0 {
		return SectionView{}, ErrSectionNotFound
	}
	if !policies.MayUpdateSettings(role) {
		return SectionView{}, ErrUpdateForbidden
	}
	for _, group := range groups {
		if group.ManagedBy != ManagedByAccount {
			return SectionView{}, ErrManagedByPlatform
		}
	}
	if actorID == uuid.Nil {
		return SectionView{}, fmt.Errorf("account settings: actor is required")
	}
	deleter, ok := s.store.(groupDeleter)
	if !ok {
		return SectionView{}, fmt.Errorf("account settings: store cannot delete a group")
	}

	section = strings.TrimSpace(section)
	err := s.activity.Within(ctx, func(ctx context.Context) error {
		id := accountID
		for _, group := range groups {
			if err := deleter.DeleteGroup(ctx, accountID, group.Name); err != nil {
				return err
			}
			meta, err := activitylog.SettingsChange(group.Name, definitionKeys(group))
			if err != nil {
				return err
			}
			if err := s.activity.Append(ctx, models.AccountActivity{
				AccountID:   &id,
				ActorUserID: actorID,
				Action:      activitylog.ActionSettingsSectionReset,
				TargetType:  activitylog.TargetSettings,
				TargetID:    section,
				Metadata:    meta,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return SectionView{}, err
	}
	for _, group := range groups {
		s.cache.Forget(cacheKey(accountID, group.Name))
	}
	return s.renderSection(ctx, accountID, role, groups)
}

// FlushSection drops the cached rows of every account-managed group on one
// page, so the next read sees an edit made outside this service. Stored rows
// stay. An unknown page, including a platform-only page, is ErrSectionNotFound
// before the role check. A page that holds a platform-managed group is
// ErrManagedByPlatform and its cache is left in place. The activity vocabulary
// has no flush event, so this writes nothing.
func (s *Service) FlushSection(ctx context.Context, accountID uuid.UUID, role, section string) error {
	if err := requireAccount(ctx, accountID); err != nil {
		return err
	}
	groups := accountGroupsInSection(section)
	if len(groups) == 0 {
		return ErrSectionNotFound
	}
	if !policies.MayUpdateSettings(role) {
		return ErrUpdateForbidden
	}
	for _, group := range groups {
		if group.ManagedBy != ManagedByAccount {
			return ErrManagedByPlatform
		}
	}
	for _, group := range groups {
		s.cache.Forget(cacheKey(accountID, group.Name))
	}
	return nil
}

func accountGroupsInSection(section string) []Group {
	var groups []Group
	for _, group := range GroupsInSection(section) {
		if group.Scope != ScopeAccount {
			continue
		}
		groups = append(groups, group)
	}
	return groups
}

func definitionKeys(group Group) []string {
	keys := make([]string, 0, len(group.Settings))
	for _, definition := range group.Settings {
		keys = append(keys, definition.Key)
	}
	return keys
}

func (s *Service) renderSection(ctx context.Context, accountID uuid.UUID, role string, groups []Group) (SectionView, error) {
	if len(groups) == 0 {
		return SectionView{}, ErrSectionNotFound
	}
	view := SectionView{Name: groups[0].SectionName(), Blocks: []BlockView{}}
	blockAt := map[string]int{}
	for _, group := range groups {
		rendered, err := s.groupView(ctx, accountID, role, group)
		if err != nil {
			return SectionView{}, err
		}
		blockIndex, ok := blockAt[group.Block]
		if !ok {
			blockIndex = len(view.Blocks)
			blockAt[group.Block] = blockIndex
			view.Blocks = append(view.Blocks, BlockView{Title: group.Block, Groups: []GroupView{}})
		}
		view.Blocks[blockIndex].Groups = append(view.Blocks[blockIndex].Groups, rendered)
	}
	return view, nil
}

func (s *Service) groupView(ctx context.Context, accountID uuid.UUID, role string, group Group) (GroupView, error) {
	rows, err := s.store.ListGroup(ctx, accountID, group.Name)
	if err != nil {
		return GroupView{}, err
	}
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
		CanUpdate: policies.MayUpdateSettings(role) && group.ManagedBy == ManagedByAccount,
		Fields:    fields,
	}
	if !updatedAt.IsZero() {
		view.UpdatedAt = &updatedAt
	}
	return view, nil
}

func (s *Service) storedValues(ctx context.Context, accountID uuid.UUID, group string) (map[string]string, error) {
	rows, err := s.store.ListGroup(ctx, accountID, group)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		out[row.Key] = row.Value
	}
	return out, nil
}

// prepareValue casts one incoming value. skip is true when a secret is blank:
// the form never received the stored secret, so a blank field means keep it.
func (s *Service) prepareValue(definition Definition, incoming any) (value string, skip bool, err error) {
	if definition.Secret {
		if incoming == nil {
			return "", true, nil
		}
		text, ok := incoming.(string)
		if !ok {
			return "", false, fmt.Errorf("expected string")
		}
		if strings.TrimSpace(text) == "" {
			return "", true, nil
		}
		sealed, sealErr := s.sealer.Seal(text)
		if sealErr != nil {
			slog.Error("account settings seal failed", "key", definition.Key)
			return "", false, errSealFailed
		}
		return sealed, false, nil
	}
	cast, err := castIn(incoming, definition)
	if err != nil {
		return "", false, err
	}
	return cast, false, nil
}

func effectiveNonSecrets(definitions []Definition, stored, writes map[string]string) map[string]string {
	out := make(map[string]string, len(definitions))
	for _, definition := range definitions {
		if definition.Secret {
			continue
		}
		if value, ok := writes[definition.Key]; ok {
			out[definition.Key] = value
			continue
		}
		if value, ok := stored[definition.Key]; ok {
			out[definition.Key] = value
			continue
		}
		out[definition.Key] = defaultStored(definition)
	}
	return out
}

func requireAccount(ctx context.Context, accountID uuid.UUID) error {
	if ctx == nil {
		return fmt.Errorf("account settings: context is required")
	}
	if accountID == uuid.Nil {
		return fmt.Errorf("account settings: account id is required")
	}
	return nil
}

func cacheKey(accountID uuid.UUID, group string) string {
	return cacheKeyPrefix + accountID.String() + ":" + group
}
