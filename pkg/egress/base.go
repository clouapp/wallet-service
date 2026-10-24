package egress

import (
	"context"
	"net"
	"net/url"
	"strings"
)

// ReasonBaseURL is a base URL that is not an origin with an optional path: it
// carries a query, a fragment or an empty port.
const ReasonBaseURL = "the url must be an https origin with an optional path, without a query, a fragment or an empty port"

// ParsePublicBase returns raw, trimmed and parsed, when it is a base URL a service
// on the internet can post to: a destination the zero Policy allows (https, no user
// name or password, a public host, never a numeric-form, internal, metadata,
// loopback, private, link-local or reserved one, a valid port other than 22 and
// 25), and an origin with an optional path, without a query, a fragment (an empty
// one included) or an empty port.
//
// It is where a provider on the internet posts to this service, in every
// environment, so the outbound webhook settings do not apply to it: neither their
// relaxations (WEBHOOK_ALLOW_HTTP, the APP_ENV=testing loopback) nor the
// restriction WEBHOOK_ALLOWED_PORTS, which limits the ports this service posts its
// own webhooks to. The base URL's port is the one the deployment's public listener
// serves, chosen by whoever runs it; this service never connects to it.
//
// It resolves nothing: a caller with a context checks what the host resolves to
// with PublicBaseGuard.CheckPublicBase. Every refusal matches ErrRefused and names
// the rule, never the URL.
func ParsePublicBase(raw string) (*url.URL, error) {
	return parseBase(raw, Policy{})
}

// parseBase is ParsePublicBase under policy.
func parseBase(raw string, policy Policy) (*url.URL, error) {
	trimmed := strings.TrimSpace(raw)
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return nil, refuse(ReasonInvalidURL)
	}
	if err := policy.checkParsedURL(parsed); err != nil {
		return nil, err
	}
	if parsed.RawQuery != "" || parsed.ForceQuery || strings.Contains(trimmed, "#") || strings.HasSuffix(parsed.Host, ":") {
		return nil, refuse(ReasonBaseURL)
	}
	return parsed, nil
}

// PublicBaseGuard checks a base URL a provider on the internet posts to (the
// ingest base URL): the rule of ParsePublicBase and what its host name resolves to
// now, both by the zero Policy, which no deployment setting changes. It has a type
// and a method of its own, so no other guard (a webhook receiver's, an RPC node's)
// can stand in for it where a base URL is checked.
//
// The lookup is a check at that moment only. The provider resolves the name itself
// on every delivery and this service never connects to it, so a record that points
// elsewhere later (DNS rebinding) cannot be refused on this side, unlike an outbound
// webhook, whose every connection Guard.DialContext checks.
type PublicBaseGuard struct {
	guard *Guard
}

// NewPublicBaseGuard builds the guard over resolver; nil is the system's
// (net.DefaultResolver).
func NewPublicBaseGuard(resolver *net.Resolver) *PublicBaseGuard {
	return &PublicBaseGuard{guard: NewGuard(Policy{}, resolver)}
}

// CheckPublicBase refuses raw when it is not a public base URL (ParsePublicBase,
// under the guard's policy), or when its host name resolves now to an address that
// policy refuses, with ReasonResolved whatever the range. A name that does not
// resolve within the caller's deadline or 2 s is not refused (Guard.CheckDestination).
func (g *PublicBaseGuard) CheckPublicBase(ctx context.Context, raw string) error {
	if _, err := parseBase(raw, g.guard.policy); err != nil {
		return err
	}
	return g.guard.CheckDestination(ctx, strings.TrimSpace(raw))
}
