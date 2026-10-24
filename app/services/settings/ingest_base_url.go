package settings

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/macrowallets/waas/app/policies"
	"github.com/macrowallets/waas/pkg/egress"
)

const (
	groupIngestPush  = "ingest_push"
	keyIngestBaseURL = "base_url"

	// ingestBaseURLInvalid stands in for a refusal that names no rule.
	ingestBaseURLInvalid = "must be a public https URL without credentials, query or fragment"
)

// ErrIngestBaseURLUnread is an ingest_push group that could not be read (the
// database, or a nil service or context). The caller decides whether the
// environment value may stand in: a dry run may, a run that creates a provider
// webhook must not.
var ErrIngestBaseURLUnread = errors.New("the ingest_push platform setting could not be read")

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
// SettingInvalid is a stored base_url the rule refuses (an edit made outside this
// service), left for the environment value.
type IngestBaseURL struct {
	URL            string
	Source         IngestBaseURLSource
	SettingInvalid bool
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
					"(no credentials, query or fragment; never an internal, loopback or metadata host, nor a name that resolves to one, " +
					"checked when saved and at ingest:subscribe --apply only: the provider resolves it on every delivery). " +
					"ingest:subscribe reads it on each run. Blank uses WEBHOOK_INGEST_BASE_URL.",
				Type:    TypeString,
				Default: func() any { return "" },
			},
		},
		Validate: validateIngestPush,
	}
}

// baseURLGuard checks a base URL by the base URL rule and what its host name
// resolves to now; *egress.PublicBaseGuard is the one implementation, so no other
// guard (a webhook receiver's, an RPC node's) can be wired in its place.
type baseURLGuard interface {
	CheckPublicBase(ctx context.Context, rawURL string) error
}

// validateIngestPush refuses a base_url a provider cannot post to, by the rule
// ingest:subscribe applies (egress.ParsePublicBase), with the rule's sentence as
// the message (it names the rule, never the URL). Blank is allowed: it hands the
// base URL back to WEBHOOK_INGEST_BASE_URL. It resolves nothing: the group
// validators take the values alone, so SavePlatform resolves the written value
// afterwards (rejectResolvedIngestBase).
func validateIngestPush(effective map[string]string) error {
	value := strings.TrimSpace(effective[keyIngestBaseURL])
	if value == "" {
		return nil
	}
	if _, err := egress.ParsePublicBase(value); err != nil {
		return ingestBaseRefusal(err)
	}
	return nil
}

// rejectResolvedIngestBase refuses a written base_url whose host name resolves now to
// an address the base URL rule refuses (an internal or a cloud metadata record), as
// ingest:subscribe --apply would, with the one resolved-name sentence
// (egress.ReasonResolved) as the message. It runs once the value passed
// validateIngestPush, and only for a value this write sets: a name that does not
// resolve now is not refused here, and the run checks it again. It is a check at the
// save only: the provider resolves the name on every delivery, which nothing here
// checks (egress.PublicBaseGuard).
func (s *Service) rejectResolvedIngestBase(ctx context.Context, writes map[string]string) error {
	value := strings.TrimSpace(writes[keyIngestBaseURL])
	if value == "" {
		return nil
	}
	if err := s.ingestBaseGuard().CheckPublicBase(ctx, value); err != nil {
		return ingestBaseRefusal(err)
	}
	return nil
}

// ingestBaseGuard is the guard the service was given, else the base URL guard over
// the system resolver: a service without one never skips the check.
func (s *Service) ingestBaseGuard() baseURLGuard {
	if s.ingestBase != nil {
		return s.ingestBase
	}
	return egress.NewPublicBaseGuard(nil)
}

// ingestBaseRefusal is the validation error of a refused base_url: the rule's
// sentence, never the URL.
func ingestBaseRefusal(err error) error {
	message := ingestBaseURLInvalid
	var refusal *egress.Refusal
	if errors.As(err, &refusal) && refusal.Reason != "" {
		message = refusal.Reason
	}
	return &ValidationError{Fields: map[string][]string{keyIngestBaseURL: {message}}}
}

// EffectiveIngestBaseURL reads ingest_push.base_url at the moment of use, through
// the platform group cache that SavePlatform forgets, so a saved value applies to
// the next read. A stored value that passes the base URL rule wins. A missing or
// blank row returns envURL, trimmed, with Source IngestBaseURLFromEnv, and so does
// a stored value that fails the rule, marked SettingInvalid; the caller checks the
// environment value itself. A group that cannot be read (or a nil service or
// context) returns the same environment value with ErrIngestBaseURLUnread, so the
// caller can refuse to act on it. The log names the group and the reason, never a
// URL.
func (s *Service) EffectiveIngestBaseURL(ctx context.Context, envURL string) (IngestBaseURL, error) {
	fallback := IngestBaseURL{URL: strings.TrimSpace(envURL), Source: IngestBaseURLFromEnv}
	if s == nil {
		return fallback, fmt.Errorf("%w: %w", ErrIngestBaseURLUnread, errServiceRequired)
	}
	if ctx == nil {
		return fallback, fmt.Errorf("%w: context is required", ErrIngestBaseURLUnread)
	}
	stored, err := s.platformValues(ctx, groupIngestPush)
	if err != nil {
		slog.Warn("ingest base URL setting was not read", "group", groupIngestPush)
		return fallback, fmt.Errorf("%w: %w", ErrIngestBaseURLUnread, err)
	}
	value := strings.TrimSpace(stored[keyIngestBaseURL])
	if value == "" {
		return fallback, nil
	}
	if !publicIngestBase(value) {
		slog.Warn("ingest base URL setting is not a public https URL; the environment value is used",
			"group", groupIngestPush, "key", keyIngestBaseURL)
		fallback.SettingInvalid = true
		return fallback, nil
	}
	return IngestBaseURL{URL: value, Source: IngestBaseURLFromSetting}, nil
}

// publicIngestBase reports whether a stored base_url passes the base URL rule.
func publicIngestBase(value string) bool {
	_, err := egress.ParsePublicBase(value)
	return err == nil
}
