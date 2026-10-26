package settings

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// The operations groups move the HTTP rate limits (THROTTLE_*), the stale outbound
// threshold (WITHDRAWAL_STALE_AFTER_HOURS) and the front-end base of the links the
// service mails (APP_FRONTEND_URL) to the platform. Each field no row stores keeps
// its environment value, and every value is read at the moment of use.
const (
	GroupHTTPThrottle = "http_throttle"
	GroupWithdrawals  = "withdrawals"
	GroupPlatformURLs = "platform_urls"

	sectionOperations = "operations"

	// The http_throttle keys, one per THROTTLE_* variable.
	KeyThrottleLoginPerIPEmail   = "login_per_ip_email"
	KeyThrottleLoginPerIP        = "login_per_ip"
	KeyThrottleRecoverPerIPEmail = "recover_per_ip_email"
	KeyThrottleAuthPerIP         = "auth_per_ip"
	KeyThrottleAPIPerMinute      = "api_per_minute"

	keyStaleAfterHours = "stale_after_hours"
	keyFrontendURL     = "frontend_url"

	maxThrottlePerMinute = 1_000_000
	maxStaleAfterHours   = 24 * 365
)

// throttleKeys are the http_throttle keys with the variable each one overrides.
var throttleKeys = []struct{ key, env, label, help string }{
	{KeyThrottleLoginPerIPEmail, "THROTTLE_LOGIN_PER_IP_EMAIL", "Login per IP and email", "Login attempts per minute for one IP and one email."},
	{KeyThrottleLoginPerIP, "THROTTLE_LOGIN_PER_IP", "Login per IP", "Login attempts per minute for one IP."},
	{KeyThrottleRecoverPerIPEmail, "THROTTLE_RECOVER_PER_IP_EMAIL", "Recovery per IP and email", "Password recovery requests per minute for one IP and one email."},
	{KeyThrottleAuthPerIP, "THROTTLE_AUTH_PER_IP", "Auth routes per IP", "Requests per minute for one IP to each other /v1/auth route that is limited (register, 2FA, refresh, recovery, invites)."},
	{KeyThrottleAPIPerMinute, "THROTTLE_API_PER_MINUTE", "External API per token", "Requests per minute for one API token on /api/v1."},
}

func httpThrottleGroup() Group {
	definitions := make([]Definition, 0, len(throttleKeys))
	for _, entry := range throttleKeys {
		definitions = append(definitions, Definition{
			Key:   entry.key,
			Label: entry.label,
			Help: entry.help + " -1 turns the limit off; 0 or unset keeps " + entry.env + ". " +
				"Applies to the next request (the settings cache is cleared on save).",
			Type:    TypeInt,
			Default: func() any { return 0 },
		})
	}
	return Group{
		Name:     GroupHTTPThrottle,
		Scope:    ScopePlatform,
		Section:  sectionOperations,
		Block:    "Rate limits",
		Settings: definitions,
		Validate: validateHTTPThrottle,
	}
}

func validateHTTPThrottle(effective map[string]string) error {
	fields := map[string][]string{}
	for _, entry := range throttleKeys {
		if _, ok := blankOrBounded(effective[entry.key], -1, maxThrottlePerMinute); !ok {
			fields[entry.key] = []string{fmt.Sprintf("must be between -1 and %d", maxThrottlePerMinute)}
		}
	}
	if len(fields) == 0 {
		return nil
	}
	return &ValidationError{Fields: fields}
}

// ThrottleLimits is the stored http_throttle row: the attempts per minute of each key
// it stores, -1 when the limit is off. A key it does not store (or stores as 0, or an
// invalid value) is absent, so the caller keeps the environment value.
type ThrottleLimits map[string]int

// EffectiveThrottle reads http_throttle at the moment of use. A database failure is
// returned so the caller keeps the environment limits.
func (s *Service) EffectiveThrottle(ctx context.Context) (ThrottleLimits, error) {
	if s == nil {
		return nil, errServiceRequired
	}
	if ctx == nil {
		return nil, fmt.Errorf("throttle settings: context is required")
	}
	stored, err := s.platformValues(ctx, GroupHTTPThrottle)
	if err != nil {
		return nil, err
	}
	limits := ThrottleLimits{}
	for _, entry := range throttleKeys {
		parsed, ok := boundedSetting(stored[entry.key], -1, maxThrottlePerMinute)
		if ok && parsed != 0 {
			limits[entry.key] = parsed
		}
	}
	return limits, nil
}

func withdrawalsGroup() Group {
	return Group{
		Name:    GroupWithdrawals,
		Scope:   ScopePlatform,
		Section: sectionOperations,
		Block:   "Withdrawals",
		Settings: []Definition{
			{
				Key:   keyStaleAfterHours,
				Label: "Stale after",
				Help: "Hours after which a withdrawal, sweep or gas seed still pending or confirming is warned about on every " +
					"tracker pass and counted by /health. 0 keeps WITHDRAWAL_STALE_AFTER_HOURS.",
				Type:    TypeInt,
				Default: func() any { return 0 },
			},
		},
		Validate: func(effective map[string]string) error {
			if _, ok := blankOrBounded(effective[keyStaleAfterHours], 0, maxStaleAfterHours); !ok {
				return &ValidationError{Fields: map[string][]string{keyStaleAfterHours: {fmt.Sprintf("must be between 0 and %d", maxStaleAfterHours)}}}
			}
			return nil
		},
	}
}

// StaleOutboundHours is the stored withdrawals.stale_after_hours, 0 when it is not
// stored or invalid (the environment value stands).
func (s *Service) StaleOutboundHours(ctx context.Context) (int, error) {
	if s == nil {
		return 0, errServiceRequired
	}
	if ctx == nil {
		return 0, fmt.Errorf("withdrawal settings: context is required")
	}
	stored, err := s.platformValues(ctx, GroupWithdrawals)
	if err != nil {
		return 0, err
	}
	hours, ok := boundedSetting(stored[keyStaleAfterHours], 1, maxStaleAfterHours)
	if !ok {
		return 0, nil
	}
	return hours, nil
}

func platformURLsGroup() Group {
	return Group{
		Name:    GroupPlatformURLs,
		Scope:   ScopePlatform,
		Section: sectionOperations,
		Block:   "Links",
		Settings: []Definition{
			{
				Key:   keyFrontendURL,
				Label: "Front-end URL",
				Help: "Base of the links the service mails (invites: <base>/accept-invite?token=…). " +
					"An absolute http(s) URL; unset keeps APP_FRONTEND_URL.",
				Type:    TypeString,
				Default: func() any { return "" },
			},
		},
		Validate: func(effective map[string]string) error {
			raw := strings.TrimSpace(effective[keyFrontendURL])
			if raw == "" {
				return nil
			}
			if _, ok := frontendBase(raw); !ok {
				return &ValidationError{Fields: map[string][]string{keyFrontendURL: {"must be an absolute http or https URL without a query or fragment"}}}
			}
			return nil
		},
	}
}

func frontendBase(raw string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", false
	}
	return strings.TrimRight(parsed.String(), "/"), true
}

// FrontendURL is the stored platform_urls.frontend_url, "" when no usable row
// stores one (the caller keeps APP_FRONTEND_URL). A database failure is returned.
func (s *Service) FrontendURL(ctx context.Context) (string, error) {
	if s == nil {
		return "", errServiceRequired
	}
	if ctx == nil {
		return "", fmt.Errorf("platform url settings: context is required")
	}
	stored, err := s.platformValues(ctx, GroupPlatformURLs)
	if err != nil {
		return "", err
	}
	base, ok := frontendBase(stored[keyFrontendURL])
	if !ok {
		return "", nil
	}
	return base, nil
}

// scanAlertOverride is a stored positive alert threshold of deposit_scan, 0 when it
// is missing or invalid.
func scanAlertOverride(stored map[string]string, key string) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(stored[key]))
	if err != nil || parsed <= 0 {
		return 0
	}
	return parsed
}

// blankOrBounded is boundedSetting where a blank value reads as 0 (the field's
// default: keep the environment value).
func blankOrBounded(raw string, floor, ceiling int) (int, bool) {
	if strings.TrimSpace(raw) == "" {
		return 0, true
	}
	return boundedSetting(raw, floor, ceiling)
}
