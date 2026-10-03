package features

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/policies"
	activitylog "github.com/macrowallets/waas/app/services/activity"
)

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

// NewService builds the feature-flag service. Store, admins and the activity
// log are required.
func NewService(store Store, admins PlatformAdmins, activity activitylog.Writer) *Service {
	if store == nil {
		panic("account features service: store is required")
	}
	if admins == nil {
		panic("account features service: platform admins are required")
	}
	if activity == nil {
		panic("account features service: activity log is required")
	}
	return &Service{store: store, admins: admins, activity: activity}
}

// List returns every account flag the role may read. A missing row is the
// catalog default. The call does not insert rows.
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

// SetGlobal stores one platform flag and returns the row it just wrote.
// A caller who is not a platform admin is ErrPlatformForbidden and the table
// is unchanged, including when the key is unknown. An admin's unknown key is
// ErrNotFound.
func (s *Service) SetGlobal(ctx context.Context, userID uuid.UUID, key string, enabled bool) (Flag, error) {
	if err := requireUser(ctx, userID); err != nil {
		return Flag{}, err
	}
	if err := s.requirePlatformAdmin(ctx, userID); err != nil {
		return Flag{}, err
	}
	key = strings.TrimSpace(key)
	if _, ok := Find(key); !ok || !globalFlag(key) {
		return Flag{}, ErrNotFound
	}
	var flag Flag
	err := s.activity.Within(ctx, func(ctx context.Context) error {
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
		meta, err := activitylog.FeatureChange(key, value)
		if err != nil {
			return err
		}
		return s.activity.Append(ctx, models.AccountActivity{
			ActorUserID: userID,
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
