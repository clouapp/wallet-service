package settings

import (
	"context"
	"strings"
)

// EtherscanKeyForHeight returns the opened provider_etherscan API key when
// that group is enabled and the sealed api_key opens. A missing row, enabled
// false, an invalid seal, or a failed settings read returns envKey, so a
// settings outage does not stop height checks. The key and its ciphertext
// are never logged and never placed in an error.
func (s *Service) EtherscanKeyForHeight(ctx context.Context, envKey string) string {
	fallback := strings.TrimSpace(envKey)
	opened, ok := s.openedEtherscanKey(ctx)
	if !ok {
		return fallback
	}
	trimmed := strings.TrimSpace(opened)
	if trimmed == "" || IsSealed(trimmed) {
		return fallback
	}
	return trimmed
}

// openedEtherscanKey opens provider_etherscan. ok is false for a missing
// row, a disabled group, an invalid seal, or any failed read. The error from
// the store or the sealer is dropped so it cannot carry the key into a log.
func (s *Service) openedEtherscanKey(ctx context.Context) (string, bool) {
	if s == nil || ctx == nil || s.sealer == nil {
		return "", false
	}
	stored, err := s.platformValues(ctx, groupProviderEtherscan)
	if err != nil || len(stored) == 0 {
		return "", false
	}
	if strings.TrimSpace(stored[keyProviderEnabled]) != "true" {
		return "", false
	}
	sealed := stored[keyProviderAPIKey]
	if !IsSealed(sealed) {
		return "", false
	}
	opened, err := s.sealer.Open(sealed)
	if err != nil {
		return "", false
	}
	return opened, true
}
