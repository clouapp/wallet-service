package settings

import (
	"context"
	"log/slog"
	"strings"

	"github.com/macrowallets/waas/app/policies"
	"github.com/macrowallets/waas/pkg/publicurl"
)

const (
	groupIngestPush  = "ingest_push"
	keyIngestBaseURL = "base_url"

	ingestBaseURLInvalid = "must be a public https URL without credentials, query or fragment"
)

// IngestBaseURLSource names where the ingest base URL in effect was read.
type IngestBaseURLSource string

const (
	// IngestBaseURLFromSetting is a valid ingest_push.base_url platform row.
	IngestBaseURLFromSetting IngestBaseURLSource = "setting"
	// IngestBaseURLFromEnv is the environment value the caller passed
	// (WEBHOOK_INGEST_BASE_URL, vault.webhooks.ingest_base_url).
	IngestBaseURLFromEnv IngestBaseURLSource = "env"
)

// IngestBaseURL is the origin Alchemy, Helius and QuickNode post deposits to, and
// where it was read. An empty URL is no base URL configured anywhere.
type IngestBaseURL struct {
	URL    string
	Source IngestBaseURLSource
}

// ingestPushGroup is the platform group of the provider push configuration the
// three webhook providers share. base_url is not a secret: it is returned in the
// clear. It sits beside the provider credential groups and takes their permission
// pair; a platform_admins row stands in for it.
func ingestPushGroup() Group {
	return Group{
		Name:             groupIngestPush,
		Scope:            ScopePlatform,
		Section:          sectionProviders,
		Block:            blockWebhookProviders,
		ViewPermission:   policies.PermProvidersView,
		UpdatePermission: policies.PermProvidersUpdate,
		Settings: []Definition{
			{
				Key:   keyIngestBaseURL,
				Label: "Ingest base URL",
				Help: "Public https origin Alchemy, Helius and QuickNode post deposits to, a path prefix allowed " +
					"(no credentials, query or fragment). ingest:subscribe reads it on each run. Blank uses WEBHOOK_INGEST_BASE_URL.",
				Type:    TypeString,
				Default: func() any { return "" },
			},
		},
		Validate: validateIngestPush,
	}
}

// validateIngestPush refuses a base_url a provider cannot post to, by the rule
// ingest:subscribe applies (publicurl.ParseBase). Blank is allowed: it hands the
// base URL back to WEBHOOK_INGEST_BASE_URL.
func validateIngestPush(effective map[string]string) error {
	value := strings.TrimSpace(effective[keyIngestBaseURL])
	if value == "" {
		return nil
	}
	if _, err := publicurl.ParseBase(value); err != nil {
		return &ValidationError{Fields: map[string][]string{keyIngestBaseURL: {ingestBaseURLInvalid}}}
	}
	return nil
}

// EffectiveIngestBaseURL reads ingest_push.base_url at the moment of use, through
// the platform group cache that SavePlatform forgets, so a saved value applies to
// the next read. A stored value that passes the base URL rule wins. A missing or
// blank row, a stored value that fails the rule (an edit made outside this
// service), a failed read, a nil service or a nil context return envURL, trimmed,
// with Source IngestBaseURLFromEnv; the caller checks that value itself. The log
// names the group and the reason, never the value.
func (s *Service) EffectiveIngestBaseURL(ctx context.Context, envURL string) IngestBaseURL {
	fallback := IngestBaseURL{URL: strings.TrimSpace(envURL), Source: IngestBaseURLFromEnv}
	if s == nil || ctx == nil {
		return fallback
	}
	stored, err := s.platformValues(ctx, groupIngestPush)
	if err != nil {
		slog.Warn("ingest base URL setting was not read; the environment value is used", "group", groupIngestPush)
		return fallback
	}
	value := strings.TrimSpace(stored[keyIngestBaseURL])
	if value == "" {
		return fallback
	}
	if _, parseErr := publicurl.ParseBase(value); parseErr != nil {
		slog.Warn("ingest base URL setting is not a public https URL; the environment value is used",
			"group", groupIngestPush, "key", keyIngestBaseURL)
		return fallback
	}
	return IngestBaseURL{URL: value, Source: IngestBaseURLFromSetting}
}
