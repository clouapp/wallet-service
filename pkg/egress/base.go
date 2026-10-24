package egress

import (
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
// with Guard.CheckDestination over the zero Policy. Every refusal matches
// ErrRefused and names the rule, never the URL.
func ParsePublicBase(raw string) (*url.URL, error) {
	trimmed := strings.TrimSpace(raw)
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return nil, refuse(ReasonInvalidURL)
	}
	if err := (Policy{}).checkParsedURL(parsed); err != nil {
		return nil, err
	}
	if parsed.RawQuery != "" || parsed.ForceQuery || strings.Contains(trimmed, "#") || strings.HasSuffix(parsed.Host, ":") {
		return nil, refuse(ReasonBaseURL)
	}
	return parsed, nil
}
