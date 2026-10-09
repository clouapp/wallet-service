package features

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/policies"
	activitylog "github.com/macrowallets/waas/app/services/activity"
)

// Accounts finds one account. A missing row is models.ErrRepositoryNotFound.
// The platform scope read uses it after the admin check.
type Accounts interface {
	FindByID(ctx context.Context, id uuid.UUID) (*models.Account, error)
}

// Store reads and writes flag rows. It does not know the catalog.
type Store interface {
	ListAccount(ctx context.Context, accountID uuid.UUID) ([]models.Feature, error)
	Upsert(ctx context.Context, accountID uuid.UUID, key string, enabled bool) error
	ListGlobal(ctx context.Context) ([]models.GlobalFeature, error)
	GetGlobal(ctx context.Context, key string) (enabled bool, found bool, err error)
	UpsertGlobal(ctx context.Context, key string, enabled bool) error
}

// PlatformAdmins reports whether a dashboard user may manage platform flags.
// A missing row is not an admin. The check is not cached.
type PlatformAdmins interface {
	Contains(ctx context.Context, userID uuid.UUID) (bool, error)
}

// Service reads and writes feature flags. A read with no row is the catalog
// default. A write is stored and then read back; the response is that row,
// not the request echoed before the write.
type Service struct {
	store    Store
	admins   PlatformAdmins
	activity activitylog.Writer
}

// Deps is everything the feature-flag service needs. Store, Admins, and
// Activity are required.
type Deps struct {
	Store    Store
	Admins   PlatformAdmins
	Activity activitylog.Writer
}

// NewService builds the feature-flag service. Store, admins and the activity
// log are required.
func NewService(deps Deps) *Service {
	if deps.Store == nil {
		panic("account features service: store is required")
	}
	if deps.Admins == nil {
		panic("account features service: platform admins are required")
	}
	if deps.Activity == nil {
		panic("account features service: activity log is required")
	}
	return &Service{store: deps.Store, admins: deps.Admins, activity: deps.Activity}
}

// List returns every account flag the role may read.
// GET /v1/accounts/{accountId}/features applies policies.MayViewSettings
// (settings.read) before the handler. This method still checks: owner, admin
// and auditor may read, and user is ErrViewForbidden before any lookup. A
// missing row is the catalog default. The call does not insert rows.
func (s *Service) List(ctx context.Context, accountID uuid.UUID, role string) (List, error) {
	if err := requireAccount(ctx, accountID); err != nil {
		return List{}, err
	}
	if !policies.MayViewSettings(role) {
		return List{}, ErrViewForbidden
	}
	stored, err := s.stored(ctx, accountID)
	if err != nil {
		return List{}, err
	}
	flags := make([]Flag, 0, len(catalog))
	for _, definition := range ForAccount() {
		flags = append(flags, Flag{
			Key:     definition.Key,
			Enabled: enabledValue(stored, definition),
		})
	}
	return List{Features: flags}, nil
}

// Set stores one flag and returns the row it just wrote. An unknown key is
// ErrNotFound before the permission check. Auditor and user are
// ErrUpdateForbidden and leave the table unchanged.
func (s *Service) Set(ctx context.Context, accountID, actorID uuid.UUID, role, key string, enabled bool) (Flag, error) {
	if err := requireAccount(ctx, accountID); err != nil {
		return Flag{}, err
	}
	key = strings.TrimSpace(key)
	if _, ok := Find(key); !ok || !accountFlag(key) {
		return Flag{}, ErrNotFound
	}
	if !policies.MayUpdateSettings(role) {
		return Flag{}, ErrUpdateForbidden
	}
	if actorID == uuid.Nil {
		return Flag{}, fmt.Errorf("account features: actor is required")
	}
	var flag Flag
	err := s.activity.Within(ctx, func(ctx context.Context) error {
		if err := s.store.Upsert(ctx, accountID, key, enabled); err != nil {
			return err
		}
		stored, err := s.stored(ctx, accountID)
		if err != nil {
			return err
		}
		value, ok := stored[key]
		if !ok {
			return ErrNotStored
		}
		flag = Flag{Key: key, Enabled: value}
		meta, err := activitylog.FeatureChange(key, value)
		if err != nil {
			return err
		}
		id := accountID
		return s.activity.Append(ctx, models.AccountActivity{
			AccountID:   &id,
			ActorUserID: actorID,
			Action:      activitylog.ActionFeaturesUpdated,
			TargetType:  activitylog.TargetFeature,
			TargetID:    key,
			Metadata:    meta,
		})
	})
	if err != nil {
		return Flag{}, err
	}
	return flag, nil
}

// ListGlobal returns every platform flag a platform admin may read. A missing
// row is the catalog default, the same default the account scope uses. The
// call does not insert rows. Anyone else receives ErrPlatformForbidden.
func (s *Service) ListGlobal(ctx context.Context, userID uuid.UUID) (List, error) {
	if err := requireUser(ctx, userID); err != nil {
		return List{}, err
	}
	if err := s.requirePlatformAdmin(ctx, userID); err != nil {
		return List{}, err
	}
	stored, err := s.globalStored(ctx)
	if err != nil {
		return List{}, err
	}
	flags := make([]Flag, 0, len(catalog))
	for _, definition := range ForGlobal() {
		flags = append(flags, Flag{
			Key:     definition.Key,
			Enabled: enabledValue(stored, definition),
		})
	}
	return List{Features: flags}, nil
}

// ActiveGlobal returns the flag keys whose platform value is on, in catalog
// order. A missing global row uses the catalog default, so
// deposit-scan-enabled, sweep-enabled, wallet-creation-enabled,
// webhook-delivery-enabled, and withdrawals-enabled are included
// until a row stores false. Account rows
// are not read: this is the global half of GET /v1/users/me. The catalog has
// no user scope, so there is nothing further to add. The call does not
// insert rows and does not cache.
func (s *Service) ActiveGlobal(ctx context.Context) ([]string, error) {
	if s == nil {
		return nil, fmt.Errorf("platform features: service is required")
	}
	if ctx == nil {
		return nil, fmt.Errorf("platform features: context is required")
	}
	stored, err := s.globalStored(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0)
	for _, definition := range ForGlobal() {
		if enabledValue(stored, definition) {
			names = append(names, definition.Key)
		}
	}
	return names, nil
}

// ActiveForAccount returns the flag keys whose account value is on, in
// catalog order. A missing account row uses the catalog default, so
// deposit-scan-enabled, sweep-enabled, wallet-creation-enabled,
// webhook-delivery-enabled, and withdrawals-enabled are included until a
// row stores false. Global rows are not read: this is the account half of
// GET /v1/accounts/{accountId}. The call does not insert rows and does not
// cache.
func (s *Service) ActiveForAccount(ctx context.Context, accountID uuid.UUID) ([]string, error) {
	if s == nil {
		return nil, fmt.Errorf("account features: service is required")
	}
	if err := requireAccount(ctx, accountID); err != nil {
		return nil, err
	}
	stored, err := s.stored(ctx, accountID)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0)
	for _, definition := range ForAccount() {
		if enabledValue(stored, definition) {
			names = append(names, definition.Key)
		}
	}
	return names, nil
}

// SetGlobal stores one platform flag and returns the row it just wrote.
// An unknown key is ErrNotFound before the platform-admin check, and the
// table is unchanged. A caller who is not a platform admin is
// ErrPlatformForbidden after the key is known.
func (s *Service) SetGlobal(ctx context.Context, userID uuid.UUID, key string, enabled bool) (Flag, error) {
	if err := requireUser(ctx, userID); err != nil {
		return Flag{}, err
	}
	key = strings.TrimSpace(key)
	if _, ok := Find(key); !ok || !globalFlag(key) {
		return Flag{}, ErrNotFound
	}
	if err := s.requirePlatformAdmin(ctx, userID); err != nil {
		return Flag{}, err
	}
	var flag Flag
	err := s.activity.Within(ctx, func(ctx context.Context) error {
		stored, err := s.globalStored(ctx)
		if err != nil {
			return err
		}
		previous, err := flagValue(stored, key)
		if err != nil {
			return err
		}
		if err := s.store.UpsertGlobal(ctx, key, enabled); err != nil {
			return err
		}
		value, found, err := s.store.GetGlobal(ctx, key)
		if err != nil {
			return err
		}
		if !found {
			return ErrNotStored
		}
		flag = Flag{Key: key, Enabled: value}
		return s.appendFeatureAudit(ctx, nil, userID, activitylog.ActionFeaturesGlobalUpdated, map[string]bool{key: previous}, map[string]bool{key: value})
	})
	if err != nil {
		return Flag{}, err
	}
	return flag, nil
}

// ListScopedForPlatform is GET /v1/platform/features/{scope}/{id}.
// S2.4 names features.view and scope account|user|chain, and refuses global.
// This catalog stores account and global rows only, so user, chain, global,
// and any other scope are ErrScopeNotFound before the admin check and before
// the account is read. An account id that is not a UUID is
// ErrInvalidAccountID before the admin check. A missing account is
// ErrAccountNotFound before the admin check. A caller who is not a platform
// admin is ErrPlatformForbidden after that account is read. A missing flag
// row is the catalog default
// and is not inserted. Global rows are not applied, so a closed global veto
// does not make an account default look saved.
func (s *Service) ListScopedForPlatform(ctx context.Context, actorID uuid.UUID, scope, rawID string, accounts Accounts) (List, error) {
	if s == nil {
		return List{}, fmt.Errorf("platform features: service is required")
	}
	if err := requireUser(ctx, actorID); err != nil {
		return List{}, err
	}
	scope = strings.TrimSpace(scope)
	if scope != ScopeAccount {
		return List{}, ErrScopeNotFound
	}
	accountID, err := uuid.Parse(strings.TrimSpace(rawID))
	if err != nil || accountID == uuid.Nil {
		return List{}, ErrInvalidAccountID
	}
	if accounts == nil {
		return List{}, fmt.Errorf("platform features: accounts are required")
	}
	account, err := accounts.FindByID(ctx, accountID)
	if err != nil {
		if errors.Is(err, models.ErrRepositoryNotFound) {
			return List{}, ErrAccountNotFound
		}
		return List{}, err
	}
	if account == nil || account.ID != accountID {
		return List{}, ErrAccountNotFound
	}
	if err := s.requirePlatformAdmin(ctx, actorID); err != nil {
		return List{}, err
	}
	stored, err := s.stored(ctx, account.ID)
	if err != nil {
		return List{}, err
	}
	flags := make([]Flag, 0, len(catalog))
	for _, definition := range ForAccount() {
		flags = append(flags, Flag{
			Key:     definition.Key,
			Enabled: enabledValue(stored, definition),
		})
	}
	return List{Features: flags}, nil
}

// ScopedWrite is one account flag a platform admin asked to store.
type ScopedWrite struct {
	Key     string
	Enabled bool
}

// SetScopedForPlatform is PUT /v1/platform/features/{scope}/{id}[/{feature}].
// S2.4 names FeaturePolicy features.update for any scope and
// features.account.update for the account scope. Neither name is a
// permission row, so a platform_admins row is the gate and stands in for
// both. The pair is not a second gate. This catalog stores account rows
// on this path, so user, chain, global, and any other scope are
// ErrScopeNotFound before the admin check and before the account is read.
// An account id that is not a UUID is ErrInvalidAccountID before the admin
// check. A missing account is ErrAccountNotFound before the admin check.
// An unknown key is ErrNotFound before the admin check. A caller who is
// not a platform admin is ErrPlatformForbidden after the account and the
// keys are known. Every key is checked before the first
// upsert: an unknown key, or a key that does not apply to the account
// scope, is ErrNotFound and nothing is stored. A duplicate key is
// ErrDuplicateWrite and nothing is stored. The boolean that is stored is
// the account row. A closed global row is not applied and is not written.
// A flag omitted from the request is not inserted.
func (s *Service) SetScopedForPlatform(ctx context.Context, actorID uuid.UUID, scope, rawID string, writes []ScopedWrite, accounts Accounts) (List, error) {
	if s == nil {
		return List{}, fmt.Errorf("platform features: service is required")
	}
	if err := requireUser(ctx, actorID); err != nil {
		return List{}, err
	}
	scope = strings.TrimSpace(scope)
	if scope != ScopeAccount {
		return List{}, ErrScopeNotFound
	}
	accountID, err := uuid.Parse(strings.TrimSpace(rawID))
	if err != nil || accountID == uuid.Nil {
		return List{}, ErrInvalidAccountID
	}
	if len(writes) == 0 {
		return List{}, fmt.Errorf("platform features: at least one flag is required")
	}
	if accounts == nil {
		return List{}, fmt.Errorf("platform features: accounts are required")
	}
	account, err := accounts.FindByID(ctx, accountID)
	if err != nil {
		if errors.Is(err, models.ErrRepositoryNotFound) {
			return List{}, ErrAccountNotFound
		}
		return List{}, err
	}
	if account == nil || account.ID != accountID {
		return List{}, ErrAccountNotFound
	}
	normalized, err := normalizeScopedWrites(writes)
	if err != nil {
		return List{}, err
	}
	if err := s.requirePlatformAdmin(ctx, actorID); err != nil {
		return List{}, err
	}
	flags := make([]Flag, 0, len(normalized))
	err = s.activity.Within(ctx, func(ctx context.Context) error {
		stored, err := s.stored(ctx, account.ID)
		if err != nil {
			return err
		}
		before := make(map[string]bool, len(normalized))
		for _, write := range normalized {
			previous, err := flagValue(stored, write.Key)
			if err != nil {
				return err
			}
			before[write.Key] = previous
			if err := s.store.Upsert(ctx, account.ID, write.Key, write.Enabled); err != nil {
				return err
			}
		}
		stored, err = s.stored(ctx, account.ID)
		if err != nil {
			return err
		}
		after := make(map[string]bool, len(normalized))
		for _, write := range normalized {
			value, ok := stored[write.Key]
			if !ok {
				return ErrNotStored
			}
			after[write.Key] = value
			flags = append(flags, Flag{Key: write.Key, Enabled: value})
		}
		id := account.ID
		return s.appendFeatureAudit(ctx, &id, actorID, activitylog.ActionAccountFeaturesUpdated, before, after)
	})
	if err != nil {
		return List{}, err
	}
	return List{Features: flags}, nil
}

// SetScopedFlagForPlatform is PUT /v1/platform/features/{scope}/{id}/{feature}:
// SetScopedForPlatform for one flag, returning the stored flag. It refuses and
// stores the same way. A write that reads back anything but that one flag is
// ErrNotStored.
func (s *Service) SetScopedFlagForPlatform(ctx context.Context, actorID uuid.UUID, scope, rawID, key string, enabled bool, accounts Accounts) (Flag, error) {
	view, err := s.SetScopedForPlatform(ctx, actorID, scope, rawID, []ScopedWrite{{Key: key, Enabled: enabled}}, accounts)
	if err != nil {
		return Flag{}, err
	}
	if len(view.Features) != 1 {
		return Flag{}, ErrNotStored
	}
	return view.Features[0], nil
}

// appendFeatureAudit stores one account_activity row for the flags whose
// boolean changed. An unchanged flag is not a change. A write that changes
// nothing stores the rows and writes no activity. Platform rows pass a nil
// account id. The old features.updated action is not written here.
func (s *Service) appendFeatureAudit(ctx context.Context, accountID *uuid.UUID, actorID uuid.UUID, action string, before, after map[string]bool) error {
	meta, changed, err := activitylog.FeatureAudit(before, after)
	if err != nil || !changed {
		return err
	}
	targetID, err := activitylog.FeatureAuditTarget(meta)
	if err != nil {
		return err
	}
	return s.activity.Append(ctx, models.AccountActivity{
		AccountID:   accountID,
		ActorUserID: actorID,
		Action:      action,
		TargetType:  activitylog.TargetFeature,
		TargetID:    targetID,
		Metadata:    meta,
	})
}

func flagValue(stored map[string]bool, key string) (bool, error) {
	definition, ok := Find(key)
	if !ok {
		return false, ErrNotFound
	}
	return enabledValue(stored, definition), nil
}

func normalizeScopedWrites(writes []ScopedWrite) ([]ScopedWrite, error) {
	seen := make(map[string]struct{}, len(writes))
	out := make([]ScopedWrite, 0, len(writes))
	for _, write := range writes {
		key := strings.TrimSpace(write.Key)
		if _, ok := Find(key); !ok || !accountFlag(key) {
			return nil, ErrNotFound
		}
		if _, dup := seen[key]; dup {
			return nil, ErrDuplicateWrite
		}
		seen[key] = struct{}{}
		out = append(out, ScopedWrite{Key: key, Enabled: write.Enabled})
	}
	slices.SortFunc(out, func(a, b ScopedWrite) int {
		return strings.Compare(a.Key, b.Key)
	})
	return out, nil
}

func (s *Service) stored(ctx context.Context, accountID uuid.UUID) (map[string]bool, error) {
	rows, err := s.store.ListAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(rows))
	for _, row := range rows {
		if _, known := Find(row.Key); !known {
			continue
		}
		out[row.Key] = row.Enabled
	}
	return out, nil
}

func accountFlag(key string) bool {
	definition, ok := Find(key)
	return ok && definition.AppliesTo(ScopeAccount)
}

func globalFlag(key string) bool {
	definition, ok := Find(key)
	return ok && definition.AppliesTo(ScopeGlobal)
}

func (s *Service) requirePlatformAdmin(ctx context.Context, userID uuid.UUID) error {
	ok, err := s.admins.Contains(ctx, userID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrPlatformForbidden
	}
	return nil
}

func (s *Service) globalStored(ctx context.Context) (map[string]bool, error) {
	rows, err := s.store.ListGlobal(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(rows))
	for _, row := range rows {
		if _, known := Find(row.Key); !known {
			continue
		}
		out[row.Key] = row.Enabled
	}
	return out, nil
}

// globalExplicitlyOff is the platform veto. A missing row and an explicit
// true do not block. Only a stored false does. The catalog default is not
// consulted here.
func (s *Service) globalExplicitlyOff(ctx context.Context, key string) (bool, error) {
	enabled, found, err := s.store.GetGlobal(ctx, key)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	return !enabled, nil
}

func requireUser(ctx context.Context, userID uuid.UUID) error {
	if ctx == nil {
		return fmt.Errorf("platform features: context is required")
	}
	if userID == uuid.Nil {
		return fmt.Errorf("platform features: user id is required")
	}
	return nil
}

func requireAccount(ctx context.Context, accountID uuid.UUID) error {
	if ctx == nil {
		return fmt.Errorf("account features: context is required")
	}
	if accountID == uuid.Nil {
		return fmt.Errorf("account features: account id is required")
	}
	return nil
}
