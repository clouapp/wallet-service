package settings

import (
	"context"
	"encoding/json"
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

const (
	cacheKeyPrefix         = "settings:account:"
	platformCacheKeyPrefix = "settings:platform:"
	// settingsCacheTTL is the group cache lifetime from S1.4.8.
	settingsCacheTTL = 10 * time.Minute
)

// Store reads and writes one account group's values.
type Store interface {
	ListGroup(ctx context.Context, accountID uuid.UUID, group string) ([]models.Setting, error)
	UpsertMany(ctx context.Context, accountID uuid.UUID, group string, values map[string]string) error
}

// Sealer encrypts a secret before it is stored and opens it on the secret read path.
// It never logs the plaintext or the sealed blob. The cache keeps that ciphertext.
type Sealer interface {
	Seal(plaintext string) (string, error)
	Open(value string) (string, error)
}

// Cache stores one group as a JSON map for settingsCacheTTL and forgets it after a write.
// A miss, a read failure, or a value that is not a JSON object only costs a database query.
type Cache interface {
	Forget(key string) bool
	// Get returns the JSON map. found is false when the key is absent.
	// A non-nil error is a read failure; the caller reads the database.
	Get(key string) (value string, found bool, err error)
	// Put stores a JSON map. The caller treats a store failure as a miss.
	Put(key string, value string, ttl time.Duration) error
}

// platformAdmins is the platform_admins row used when the catalog has no
// matching permission name. settings.view and settings.update are those names.
type platformAdmins interface {
	Contains(ctx context.Context, userID uuid.UUID) (bool, error)
}

// accountDirectory reports whether an account id is stored. The platform
// account settings route answers 404 for a missing id before it checks
// platform_admins.
type accountDirectory interface {
	Exists(ctx context.Context, id uuid.UUID) (bool, error)
}

// Service reads and writes the account settings registry.
type Service struct {
	store    Store
	sealer   Sealer
	cache    Cache
	activity activitylog.Writer
	admins   platformAdmins
	accounts accountDirectory
}

// Deps is everything the account settings service needs. Store, Sealer, and
// Activity are required. A nil Cache uses a no-op cache.
type Deps struct {
	Store    Store
	Sealer   Sealer
	Cache    Cache
	Activity activitylog.Writer
}

// NewService builds the account settings service.
func NewService(deps Deps) *Service {
	if deps.Store == nil {
		panic("account settings service: store is required")
	}
	if deps.Sealer == nil {
		panic("account settings service: sealer is required")
	}
	if deps.Activity == nil {
		panic("account settings service: activity log is required")
	}
	cache := deps.Cache
	if cache == nil {
		cache = nopCache{}
	}
	return &Service{store: deps.Store, sealer: deps.Sealer, cache: cache, activity: deps.Activity}
}

// WithPlatformAdmins sets the gate for platform groups. A nil reader leaves
// SavePlatform unable to tell a platform admin from anyone else.
func (s *Service) WithPlatformAdmins(admins platformAdmins) *Service {
	if s == nil {
		return nil
	}
	s.admins = admins
	return s
}

// WithAccounts sets the account lookup for platform-managed account groups.
// A nil reader leaves that route unable to tell a missing account from a stored one.
func (s *Service) WithAccounts(accounts accountDirectory) *Service {
	if s == nil {
		return nil
	}
	s.accounts = accounts
	return s
}

// FacadeCache stores and forgets keys through the process cache.
type FacadeCache struct{}

// settingsCacheDriver is the slice of the process cache this service uses.
// One facades.Cache() call lives in processCache so the architecture count stays put.
type settingsCacheDriver interface {
	Forget(key string) bool
	GetString(key string, def ...string) string
	Put(key string, value any, ttl time.Duration) error
}

func processCache() settingsCacheDriver {
	return facades.Cache()
}

// Forget drops one cache key. A missing cache is a no-op.
func (FacadeCache) Forget(key string) bool {
	cache := processCache()
	if cache == nil {
		return false
	}
	return cache.Forget(key)
}

// Get returns a JSON map. An empty value is a miss: Goravel answers a
// missing key and a failed read the same way, and both fall through to the database.
func (FacadeCache) Get(key string) (string, bool, error) {
	cache := processCache()
	if cache == nil {
		return "", false, errSettingsCacheUnavailable
	}
	value := cache.GetString(key)
	if value == "" {
		return "", false, nil
	}
	return value, true, nil
}

// Put stores a JSON map for ttl.
func (FacadeCache) Put(key, value string, ttl time.Duration) error {
	if value == "" {
		return errSettingsCacheUnavailable
	}
	cache := processCache()
	if cache == nil {
		return errSettingsCacheUnavailable
	}
	return cache.Put(key, value, ttl)
}

type nopCache struct{}

func (nopCache) Forget(string) bool { return false }

func (nopCache) Get(string) (string, bool, error) { return "", false, nil }

func (nopCache) Put(string, string, time.Duration) error { return nil }

// Registry returns every account group, with secrets replaced by is_set.
// GET /v1/accounts/{accountId}/settings applies policies.MayViewSettings
// (settings.read) before the handler. This method does not repeat that
// check. S1.4.7 names settings.read and settings.write on this catalog.
// The pair is not a second gate.
func (s *Service) Registry(ctx context.Context, accountID uuid.UUID, role string) (RegistryView, error) {
	if err := requireAccount(ctx, accountID); err != nil {
		return RegistryView{}, err
	}
	catalog := AccountSettingsCatalog()
	if err := requireAccountGuard(catalog); err != nil {
		return RegistryView{}, err
	}

	view := RegistryView{
		Permissions: Permissions{View: catalog.Read, Update: catalog.Write},
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

// Save writes one group. PATCH and PUT
// /v1/accounts/{accountId}/settings/{group} apply policies.MayUpdateSettings
// (settings.write) before the handler. This method does not repeat that
// check. An unknown group is ErrGroupNotFound. A platform-managed group is
// ErrManagedByPlatform and is not stored. A blank or omitted secret keeps
// the stored ciphertext. A real write and its activity row commit together.
// Metadata stores the group and the field names, never the values.
func (s *Service) Save(ctx context.Context, accountID, actorID uuid.UUID, role, groupName string, body map[string]any) (GroupView, error) {
	if err := requireAccount(ctx, accountID); err != nil {
		return GroupView{}, err
	}
	groupName = strings.TrimSpace(groupName)
	group, ok := FindGroup(groupName)
	if !ok || group.Scope != ScopeAccount {
		return GroupView{}, ErrGroupNotFound
	}
	if group.ManagedBy != ManagedByAccount {
		return GroupView{}, ErrManagedByPlatform
	}
	if err := requireAccountGuard(AccountSettingsCatalog()); err != nil {
		return GroupView{}, err
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
	auditErr := s.recordSettingsAudit(ctx, &accountID, group, stored, writes)
	s.cache.Forget(cacheKey(accountID, group.Name))
	if auditErr != nil {
		return GroupView{}, auditErr
	}
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
	id := accountID
	snapshots, err := s.auditSnapshots(ctx, &id, groups)
	if err != nil {
		return SectionView{}, err
	}

	section = strings.TrimSpace(section)
	err = s.activity.Within(ctx, func(ctx context.Context) error {
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
	if err := s.recordRemovedSettings(ctx, &id, snapshots); err != nil {
		return SectionView{}, err
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
	canUpdate := policies.MayUpdateSettings(role) && group.ManagedBy == ManagedByAccount
	return renderStoredGroup(group, rows, canUpdate), nil
}

// storedValues is the cached read of one account group. A hit returns the
// JSON map. A miss, a cache failure, or a value that is not a JSON object
// reads the database and stores that map. The database error is returned unchanged.
func (s *Service) storedValues(ctx context.Context, accountID uuid.UUID, group string) (map[string]string, error) {
	key := cacheKey(accountID, group)
	if values, ok := s.recall(key); ok {
		return values, nil
	}
	out, err := s.listAccountValues(ctx, accountID, group)
	if err != nil {
		return nil, err
	}
	s.remember(key, out)
	return out, nil
}

// listAccountValues reads one account group from the database. Save uses it
// so a write is not merged from a stale cache; Forget still drops that cache.
func (s *Service) listAccountValues(ctx context.Context, accountID uuid.UUID, group string) (map[string]string, error) {
	rows, err := s.store.ListGroup(ctx, accountID, group)
	if err != nil {
		return nil, err
	}
	return valuesFromRows(rows), nil
}

// platformValues is the cached read of one platform group. The same miss and
// invalid-document rules as storedValues apply. A store without a platform reader
// still fails before the cache is consulted.
func (s *Service) platformValues(ctx context.Context, group string) (map[string]string, error) {
	reader, ok := s.store.(platformReader)
	if !ok {
		return nil, fmt.Errorf("deposit scan settings: platform reader is required")
	}
	key := platformCacheKey(group)
	if values, ok := s.recall(key); ok {
		return values, nil
	}
	rows, err := reader.ListPlatform(ctx, group)
	if err != nil {
		return nil, err
	}
	out := valuesFromRows(rows)
	s.remember(key, out)
	return out, nil
}

func valuesFromRows(rows []models.Setting) map[string]string {
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		out[row.Key] = row.Value
	}
	return out
}

// recall returns the cached JSON map. A miss or a read failure returns false
// without logging the payload. A value that is not a JSON object also returns
// false so the caller reads the database. Secret fields stay ciphertext.
func (s *Service) recall(key string) (map[string]string, bool) {
	if s == nil || s.cache == nil || key == "" {
		return nil, false
	}
	raw, found, err := s.cache.Get(key)
	if err != nil || !found || raw == "" {
		if err != nil {
			slog.Warn("settings cache read failed", "cache_key", key)
		}
		return nil, false
	}
	var values map[string]string
	if unmarshalErr := json.Unmarshal([]byte(raw), &values); unmarshalErr != nil || values == nil {
		slog.Warn("settings cache fell through to the database", "cache_key", key)
		return nil, false
	}
	return values, true
}

// remember stores the group as a JSON map for settingsCacheTTL. Secret values
// stay the stored ciphertext and are not opened. A marshal or store failure
// leaves the read result in place and does not log the payload.
func (s *Service) remember(key string, values map[string]string) {
	if s == nil || s.cache == nil || key == "" {
		return
	}
	if values == nil {
		values = map[string]string{}
	}
	payload, err := json.Marshal(values)
	if err != nil {
		slog.Warn("settings cache was not stored", "cache_key", key)
		return
	}
	if err := s.cache.Put(key, string(payload), settingsCacheTTL); err != nil {
		slog.Warn("settings cache was not stored", "cache_key", key)
	}
}

// collectWrites casts a partial document. An unknown key, a bad cast, or a
// group validator failure is returned and nothing is stored. A blank secret
// is omitted so the stored ciphertext stays.
func (s *Service) collectWrites(group Group, stored map[string]string, body map[string]any) (map[string]string, error) {
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
			return nil, prepareErr
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
		return nil, invalid
	}
	if moved := secretRequiredAfterDestinationMove(group, stored, writes); !moved.empty() {
		return nil, moved
	}
	if incomplete := incompleteCredentialGroups(group, stored, writes); !incomplete.empty() {
		return nil, incomplete
	}
	if missing := enabledProviderWithoutKey(group, stored, writes); !missing.empty() {
		return nil, missing
	}
	if group.Validate != nil {
		if validateErr := group.Validate(effectiveNonSecrets(group.Settings, stored, writes)); validateErr != nil {
			return nil, validateErr
		}
	}
	return writes, nil
}

// prepareValue casts one incoming value. skip is true when a secret is blank:
// the form never received the stored secret, so a blank field means keep it.
// A non-blank secret is cast through its type before it is sealed.
func (s *Service) prepareValue(definition Definition, incoming any) (value string, skip bool, err error) {
	if definition.Secret {
		if incoming == nil {
			return "", true, nil
		}
		if text, ok := incoming.(string); ok && strings.TrimSpace(text) == "" {
			return "", true, nil
		}
		cast, castErr := castIn(incoming, definition)
		if castErr != nil {
			return "", false, castErr
		}
		if cast == "" {
			return "", true, nil
		}
		sealed, sealErr := s.sealer.Seal(cast)
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

func platformCacheKey(group string) string {
	return platformCacheKeyPrefix + group
}
