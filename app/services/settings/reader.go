package settings

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// String reads one non-secret string. scope nil is the platform row; a
// non-nil id is that account's row. A secret field returns ErrSecretField
// and an empty string.
func (s *Service) String(ctx context.Context, scope *uuid.UUID, group, key string) (string, error) {
	raw, definition, err := s.read(ctx, scope, group, key, false)
	if err != nil {
		return "", err
	}
	if definition.Type != TypeString {
		return "", fmt.Errorf("settings: %s.%s is not a string", group, key)
	}
	return raw, nil
}

// Int reads one non-secret int. A secret field returns ErrSecretField and zero.
func (s *Service) Int(ctx context.Context, scope *uuid.UUID, group, key string) (int, error) {
	raw, definition, err := s.read(ctx, scope, group, key, false)
	if err != nil {
		return 0, err
	}
	if definition.Type != TypeInt {
		return 0, fmt.Errorf("settings: %s.%s is not an int", group, key)
	}
	parsed, convErr := strconv.Atoi(strings.TrimSpace(raw))
	if convErr != nil {
		return 0, fmt.Errorf("settings: %s.%s is not an int", group, key)
	}
	return parsed, nil
}

// Bool reads one non-secret bool. A secret field returns ErrSecretField and false.
func (s *Service) Bool(ctx context.Context, scope *uuid.UUID, group, key string) (bool, error) {
	raw, definition, err := s.read(ctx, scope, group, key, false)
	if err != nil {
		return false, err
	}
	if definition.Type != TypeBool {
		return false, fmt.Errorf("settings: %s.%s is not a bool", group, key)
	}
	switch strings.TrimSpace(raw) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("settings: %s.%s is not a bool", group, key)
	}
}

// Secret opens one secret. A non-secret returns ErrNotASecret. An unset
// secret (no stored row, or a blank value) uses the env fallback
// platformSeedEnv already names for that field. A field with no entry stays
// empty. A bad seal returns ErrSecretSeal and not the ciphertext.
func (s *Service) Secret(ctx context.Context, scope *uuid.UUID, group, key string) (string, error) {
	raw, _, err := s.read(ctx, scope, group, key, true)
	return raw, err
}

// read loads one catalog field. wantSecret selects the plaintext path.
// String, Int, and Bool pass false and refuse a secret. Secret passes true
// and refuses a non-secret.
func (s *Service) read(ctx context.Context, scope *uuid.UUID, groupName, key string, wantSecret bool) (string, Definition, error) {
	if s == nil {
		return "", Definition{}, errServiceRequired
	}
	if ctx == nil {
		return "", Definition{}, fmt.Errorf("settings: context is required")
	}
	groupName = strings.TrimSpace(groupName)
	key = strings.TrimSpace(key)
	if groupName == "" || key == "" {
		return "", Definition{}, fmt.Errorf("settings: group and key are required")
	}
	group, ok := FindGroup(groupName)
	if !ok {
		return "", Definition{}, ErrGroupNotFound
	}
	definition, ok := Find(group.Name, key)
	if !ok {
		return "", Definition{}, fmt.Errorf("settings: unknown key")
	}
	if wantSecret != definition.Secret {
		if definition.Secret {
			return "", definition, ErrSecretField
		}
		return "", definition, ErrNotASecret
	}
	stored, err := s.valuesFor(ctx, scope, group)
	if err != nil {
		return "", definition, err
	}
	if definition.Secret {
		opened, openErr := openStoredSecret(s.sealer, group.Name, definition.Key, stored)
		return opened, definition, openErr
	}
	if raw, present := stored[definition.Key]; present {
		return raw, definition, nil
	}
	return defaultStored(definition), definition, nil
}

func (s *Service) valuesFor(ctx context.Context, scope *uuid.UUID, group Group) (map[string]string, error) {
	switch group.Scope {
	case ScopePlatform:
		if scope != nil {
			return nil, fmt.Errorf("settings: %s is a platform group", group.Name)
		}
		return s.platformValues(ctx, group.Name)
	case ScopeAccount:
		if scope == nil || *scope == uuid.Nil {
			return nil, fmt.Errorf("settings: account id is required")
		}
		stored, err := s.storedValues(ctx, *scope, group.Name)
		if err != nil {
			return nil, err
		}
		return s.withInheritedPlatform(ctx, group, stored)
	default:
		return nil, fmt.Errorf("settings: unknown scope")
	}
}

// openStoredSecret returns the opened plaintext. A missing or blank value
// uses secretEnvFallback. A value that is not sealed, or whose Open fails,
// is ErrSecretSeal. The stored text is never returned and never logged.
func openStoredSecret(sealer Sealer, group, key string, stored map[string]string) (string, error) {
	raw, set := storedSecret(stored, key)
	if !set {
		fallback, known := secretEnvFallback(group, key)
		if !known {
			return "", nil
		}
		return fallback, nil
	}
	if sealer == nil || !IsSealed(raw) {
		return "", ErrSecretSeal
	}
	opened, err := sealer.Open(raw)
	if err != nil || strings.TrimSpace(opened) == "" || IsSealed(opened) {
		return "", ErrSecretSeal
	}
	return opened, nil
}

func storedSecret(stored map[string]string, key string) (string, bool) {
	if len(stored) == 0 {
		return "", false
	}
	raw, ok := stored[key]
	if !ok || strings.TrimSpace(raw) == "" {
		return "", false
	}
	return raw, true
}

// secretEnvFallback is the env var platformSeedEnv already names for this
// field. Names that are not in that map are not read. A blank value is an
// empty fallback, not a new name.
func secretEnvFallback(group, key string) (string, bool) {
	name, ok := platformSeedEnv[group+"\x00"+key]
	if !ok || strings.TrimSpace(name) == "" {
		return "", false
	}
	return strings.TrimSpace(os.Getenv(name)), true
}
