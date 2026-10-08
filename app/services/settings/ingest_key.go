package settings

import (
	"context"
	"strings"
)

const (
	ingestProviderAlchemy   = "alchemy"
	ingestProviderHelius    = "helius"
	ingestProviderQuickNode = "quicknode"
)

// IngestProviderKey returns the opened provider credential when that group
// is enabled and the seal opens. Alchemy reads auth_token. Helius and
// QuickNode read api_key. A missing row, enabled false, an invalid seal, or
// a failed settings read returns envKey (vault.webhooks.* today). The key
// and its ciphertext are never logged and never placed in an error.
func (s *Service) IngestProviderKey(ctx context.Context, provider, envKey string) string {
	fallback := strings.TrimSpace(envKey)
	opened, ok := s.openedIngestKey(ctx, provider)
	if !ok {
		return fallback
	}
	trimmed := strings.TrimSpace(opened)
	if trimmed == "" || IsSealed(trimmed) {
		return fallback
	}
	return trimmed
}

// openedIngestKey opens one webhook ingest provider group. ok is false for
// an unknown provider, a missing row, a disabled group, an invalid seal, or
// any failed read. The store and sealer errors are dropped so they cannot
// carry the key into a log.
func (s *Service) openedIngestKey(ctx context.Context, provider string) (string, bool) {
	if s == nil || ctx == nil || s.sealer == nil {
		return "", false
	}
	group, secretKey, ok := ingestProviderGroup(provider)
	if !ok {
		return "", false
	}
	stored, err := s.platformValues(ctx, group)
	if err != nil || len(stored) == 0 {
		return "", false
	}
	if strings.TrimSpace(stored[keyProviderEnabled]) != "true" {
		return "", false
	}
	sealed := stored[secretKey]
	if !IsSealed(sealed) {
		return "", false
	}
	opened, err := s.sealer.Open(sealed)
	if err != nil {
		return "", false
	}
	return opened, true
}

func ingestProviderGroup(provider string) (group, secretKey string, ok bool) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case ingestProviderAlchemy:
		return groupProviderAlchemy, keyProviderAuthToken, true
	case ingestProviderHelius:
		return groupProviderHelius, keyProviderAPIKey, true
	case ingestProviderQuickNode:
		return groupProviderQuickNode, keyProviderAPIKey, true
	default:
		return "", "", false
	}
}
