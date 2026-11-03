package featureflag

import (
	"fmt"
	"reflect"
	"strconv"

	"github.com/macrowallets/waas/packages/rampengine/packages/featureflag/contracts"
)

// ResolveScope maps a scope value to its storage key.
//
// Rules, in order: nil -> the global key; a FlagScope -> its identifier; a
// string -> itself; integer/unsigned kinds (including named types) -> base-10
// digits; a fmt.Stringer -> String(); anything else -> ErrUnresolvableScope.
//
// A scope that resolves to the reserved global key "__global__" is rejected so
// it cannot silently alias the global scope. Distinct scope values that resolve
// to the same string share a row (e.g. int64(42) and "42" both key "42") — this
// matches Laravel Pennant; give scope types disjoint identifier spaces (e.g.
// "user:42") when that matters.
//
// Only an untyped nil is treated as the global scope. A typed-nil pointer that
// implements FlagScope (e.g. (*User)(nil)) is still passed to
// FlagScopeIdentifier; guarding against a nil receiver there is the caller's
// responsibility.
func ResolveScope(scope any) (string, error) {
	if scope == nil {
		return globalScopeKey, nil
	}
	key, err := scopeToString(scope)
	if err != nil {
		return "", err
	}
	if key == globalScopeKey {
		return "", ErrUnresolvableScope.Args(fmt.Sprintf("%q is reserved for the global scope", globalScopeKey))
	}
	return key, nil
}

func scopeToString(scope any) (string, error) {
	switch v := scope.(type) {
	case contracts.FlagScope:
		return v.FlagScopeIdentifier(), nil
	case string:
		return v, nil
	}
	rv := reflect.ValueOf(scope)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(rv.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(rv.Uint(), 10), nil
	}
	if s, ok := scope.(fmt.Stringer); ok {
		return s.String(), nil
	}
	return "", ErrUnresolvableScope.Args(fmt.Sprintf("%T", scope))
}
