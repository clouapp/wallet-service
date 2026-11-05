package ramps

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/macrowallets/waas/app/services/features"
	"github.com/macrowallets/waas/packages/rampengine/packages/featureflag/contracts"
)

// Flags adapts reads to Macro's existing flag store. Mutations stay on
// Macro's policy-checked settings endpoints; the engine has no write bypass.
type Flags struct {
	Native    *features.Service
	AccountID uuid.UUID
	Customers CustomerStore
}

var errFlagReadOnly = errors.New("ramp engine flags must be managed through Macro")

func (f *Flags) IsActive(ctx context.Context, key string, scope any) (bool, error) {
	accountID := f.AccountID
	var customerID uuid.UUID
	if f.Customers != nil {
		if holder, ok := scope.(contracts.FlagScope); ok {
			if id, err := strconv.ParseUint(strings.TrimPrefix(holder.FlagScopeIdentifier(), "user:"), 10, 64); err == nil {
				var err error
				customerID, accountID, err = f.Customers.FeatureSubject(ctx, uint(id))
				if err != nil {
					return false, err
				}
				if f.AccountID != uuid.Nil && accountID != f.AccountID {
					return false, nil
				}
			}
		}
	}
	if key == "kyc-usa-residence-enabled" || accountID == uuid.Nil {
		return f.Native.GlobalEnabled(ctx, key)
	}
	if err := f.Native.Gate(ctx, accountID, key, key); err != nil {
		var gate *features.GateError
		if errors.As(err, &gate) {
			return false, nil
		}
		return false, err
	}
	if customerID != uuid.Nil && f.Customers != nil {
		overrides, err := f.Customers.FeatureOverrides(ctx, customerID)
		if err != nil {
			return false, err
		}
		for _, row := range overrides {
			if row.Feature == key {
				return row.Enabled, nil
			}
		}
	}
	return true, nil
}
func (f *Flags) IsInactive(ctx context.Context, key string, scope any) (bool, error) {
	v, e := f.IsActive(ctx, key, scope)
	return !v, e
}
func (f *Flags) Get(ctx context.Context, key string, scope any) (any, error) {
	return f.IsActive(ctx, key, scope)
}
func (f *Flags) Active(ctx context.Context, keys []string, scope any) ([]string, error) {
	out := []string{}
	for _, key := range keys {
		v, e := f.IsActive(ctx, key, scope)
		if e != nil {
			return out, e
		}
		if v {
			out = append(out, key)
		}
	}
	return out, nil
}
func (f *Flags) GetAll(ctx context.Context, inputs map[string][]any) (map[string]map[string]any, error) {
	out := map[string]map[string]any{}
	for key, scopes := range inputs {
		out[key] = map[string]any{}
		for _, scope := range scopes {
			v, e := f.Get(ctx, key, scope)
			if e != nil {
				return nil, e
			}
			out[key]["effective"] = v
		}
	}
	return out, nil
}
func (*Flags) Define(string, contracts.Resolver, ...contracts.DefineOption) {}
func (*Flags) Defined() []string {
	out := []string{}
	for _, d := range features.All() {
		out = append(out, d.Key)
	}
	return out
}
func (*Flags) Set(context.Context, string, any, any) error        { return errFlagReadOnly }
func (*Flags) SetForAllScopes(context.Context, string, any) error { return errFlagReadOnly }
func (*Flags) Delete(context.Context, string, any) error          { return errFlagReadOnly }
func (*Flags) Purge(context.Context, []string) error              { return errFlagReadOnly }
func (*Flags) InvalidateCache(context.Context) (bool, error)      { return false, nil }
func (*Flags) Activate(context.Context, string, any) error        { return errFlagReadOnly }
func (*Flags) Deactivate(context.Context, string, any) error      { return errFlagReadOnly }

var _ contracts.Feature = (*Flags)(nil)
