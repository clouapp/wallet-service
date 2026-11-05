package settings

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/macrowallets/waas/app/policies"
	engine "github.com/macrowallets/waas/packages/rampengine/app/settings"
)

// RampGroupName separates sandbox and production credentials and rail policy
// even when both account types are served by the same API process.
func RampGroupName(group, mode string) string { return "ramp_" + group + "_" + mode }

func rampGroups() []Group {
	var groups []Group
	for _, mode := range []string{"sandbox", "production"} {
		for _, source := range engine.RampGroups() {
			group := Group{Name: RampGroupName(source.Name, mode), Scope: ScopePlatform,
				Section: "ramps", Block: mode, ViewPermission: policies.PermProvidersView,
				UpdatePermission: policies.PermProvidersUpdate}
			for _, field := range source.Settings {
				def := Definition{Key: field.Key, Label: field.Label, Help: field.Help,
					Type: Type(field.Type), Secret: field.Secret, Options: field.Options, Default: field.Default}
				if field.Type == engine.TypeRSAPublicKeyPEM {
					def.Type = TypeRSAPublicKeyPEM
				}
				if def.Key == "mode" {
					def.Default = func() any { return mode }
					def.Options = []string{mode}
				}
				group.Settings = append(group.Settings, def)
			}
			group.Validate = func(values map[string]string) error { return engine.ValidateRampGroup(source, values, mode) }
			groups = append(groups, group)
		}
	}
	return groups
}

// Raw is the non-secret representation consumed by provider readers, which
// need decimal/string-list/int settings in the same canonical stored form.
func (s *Service) Raw(ctx context.Context, scope *uuid.UUID, group, key string) (string, error) {
	value, _, err := s.read(ctx, scope, group, key, false)
	return value, err
}

func validateRSAPublicKey(text string) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	block, rest := pem.Decode([]byte(text))
	if block == nil || len(strings.TrimSpace(string(rest))) != 0 {
		return fmt.Errorf("expected a complete RSA public key PEM")
	}
	if key, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		if rsaKey, ok := key.(*rsa.PublicKey); ok && rsaKey.N.BitLen() >= 2048 {
			return nil
		}
	}
	if key, err := x509.ParsePKCS1PublicKey(block.Bytes); err == nil && key.N.BitLen() >= 2048 {
		return nil
	}
	return fmt.Errorf("expected an RSA public key of at least 2048 bits")
}

func rampCredentialsRequired(group Group, stored, writes map[string]string) *ValidationError {
	invalid := &ValidationError{}
	if !strings.HasPrefix(group.Name, "ramp_provider_") || settingValue(stored, writes, "enabled", "false") != "true" {
		return invalid
	}
	for _, definition := range group.Settings {
		if definition.Key != "api_key" && definition.Key != "webhook_secret" && definition.Key != "webhook_public_key" {
			continue
		}
		if !credentialKeyPresent(stored, writes, definition.Key) {
			invalid.add(definition.Key, providerKeyRequiredWhenEnabled)
		}
	}
	return invalid
}
