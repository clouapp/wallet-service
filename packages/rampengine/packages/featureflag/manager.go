package featureflag

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/macrowallets/waas/packages/rampengine/packages/featureflag/contracts"
)

// manager implements contracts.Feature over one driver.
type manager struct {
	store       contracts.Driver
	mu          sync.RWMutex
	definitions map[string]definition
}

// definition is what Define records for a feature.
type definition struct {
	resolver contracts.Resolver
	options  contracts.DefineOptions
}

// NewManager returns the feature-flag API backed by store.
func NewManager(store contracts.Driver) contracts.Feature {
	return &manager{store: store, definitions: make(map[string]definition)}
}

func (m *manager) Define(feature string, resolver contracts.Resolver, options ...contracts.DefineOption) {
	d := definition{resolver: resolver}
	for _, option := range options {
		option(&d.options)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.definitions[feature] = d
}

// GatedByGlobal defines a feature whose global value vetoes every scope. It is
// the kill switch shape: an operator closes the product for everyone without
// touching the overrides of each scope, and reopening it restores them as they
// were.
func GatedByGlobal() contracts.DefineOption {
	return func(o *contracts.DefineOptions) { o.GatedByGlobal = true }
}

func (m *manager) Defined() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, 0, len(m.definitions))
	for name := range m.definitions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (m *manager) definitionOf(feature string) (definition, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	d, ok := m.definitions[feature]
	return d, ok
}

func (m *manager) Get(ctx context.Context, feature string, scope any) (any, error) {
	key, err := ResolveScope(scope)
	if err != nil {
		return nil, err
	}
	return m.getByKey(ctx, feature, key, scope)
}

// getByKey resolves-on-miss and memoizes via SetIfAbsent so a concurrent
// explicit override is never clobbered.
func (m *manager) getByKey(ctx context.Context, feature, key string, scope any) (any, error) {
	value, found, err := m.store.Get(ctx, feature, key)
	if err != nil {
		return nil, err
	}
	if found {
		return value, nil
	}
	d, ok := m.definitionOf(feature)
	if !ok {
		return nil, ErrFeatureNotDefined.Args(feature)
	}
	resolved, err := d.resolver(ctx, scope)
	if err != nil {
		return nil, err
	}
	stored, err := m.store.SetIfAbsent(ctx, feature, key, resolved)
	if err != nil {
		return nil, err
	}
	if stored {
		return resolved, nil
	}
	// Lost the race to a concurrent writer/override — return the authoritative
	// stored value; if it vanished (deleted meanwhile), fall back to resolved.
	value, found, err = m.store.Get(ctx, feature, key)
	if err != nil {
		return nil, err
	}
	if found {
		return value, nil
	}
	return resolved, nil
}

func (m *manager) GetAll(ctx context.Context, features map[string][]any) (map[string]map[string]any, error) {
	result := make(map[string]map[string]any, len(features))
	for feature, scopes := range features {
		inner := make(map[string]any, len(scopes))
		for _, scope := range scopes {
			key, err := ResolveScope(scope)
			if err != nil {
				return nil, err
			}
			value, err := m.getByKey(ctx, feature, key, scope)
			if err != nil {
				return nil, err
			}
			inner[key] = value
		}
		result[feature] = inner
	}
	return result, nil
}

func (m *manager) Set(ctx context.Context, feature string, scope, value any) error {
	key, err := ResolveScope(scope)
	if err != nil {
		return err
	}
	return m.store.Set(ctx, feature, key, value)
}

func (m *manager) SetForAllScopes(ctx context.Context, feature string, value any) error {
	return m.store.SetForAllScopes(ctx, feature, value)
}

func (m *manager) Delete(ctx context.Context, feature string, scope any) error {
	key, err := ResolveScope(scope)
	if err != nil {
		return err
	}
	return m.store.Delete(ctx, feature, key)
}

func (m *manager) Purge(ctx context.Context, features []string) error {
	return m.store.Purge(ctx, features)
}

// InvalidateCache delegates to the driver when it keeps a cache leg. A driver
// that does not implement CacheInvalidator reports (false, nil): there was
// nothing to clear, which is not the same answer as having cleared it.
func (m *manager) InvalidateCache(ctx context.Context) (bool, error) {
	invalidator, ok := m.store.(contracts.CacheInvalidator)
	if !ok {
		return false, nil
	}
	if err := invalidator.InvalidateCache(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// IsActive treats anything but nil and false as on. For a feature defined
// GatedByGlobal, a scope is on only while the global value is on too.
//
// The global value is read on every call rather than folded into the scope's:
// Get memoizes what it resolves, so a combined value would freeze on its first
// read and the next global toggle would reach nobody.
func (m *manager) IsActive(ctx context.Context, feature string, scope any) (bool, error) {
	if d, ok := m.definitionOf(feature); ok && d.options.GatedByGlobal && scope != nil {
		global, err := m.Get(ctx, feature, nil)
		if err != nil {
			return false, err
		}
		if !isActive(global) {
			return false, nil
		}
	}
	value, err := m.Get(ctx, feature, scope)
	if err != nil {
		return false, err
	}
	return isActive(value), nil
}

func (m *manager) IsInactive(ctx context.Context, feature string, scope any) (bool, error) {
	active, err := m.IsActive(ctx, feature, scope)
	if err != nil {
		return false, err
	}
	return !active, nil
}

func (m *manager) Active(ctx context.Context, features []string, scope any) ([]string, error) {
	active := make([]string, 0, len(features))
	var failures []error
	for _, feature := range features {
		on, err := m.IsActive(ctx, feature, scope)
		if err != nil {
			failures = append(failures, fmt.Errorf("featureflag: resolve %q: %w", feature, err))
			continue
		}
		if on {
			active = append(active, feature)
		}
	}
	return active, errors.Join(failures...)
}

func (m *manager) Activate(ctx context.Context, feature string, scope any) error {
	return m.Set(ctx, feature, scope, true)
}

func (m *manager) Deactivate(ctx context.Context, feature string, scope any) error {
	return m.Set(ctx, feature, scope, false)
}

var _ contracts.Feature = (*manager)(nil)
