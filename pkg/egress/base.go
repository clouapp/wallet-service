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
// one included) or an empty port. Unlike a webhook receiver it takes none of a
// deployment's relaxations (WEBHOOK_ALLOW_HTTP, the APP_ENV=testing loopback,
// WEBHOOK_ALLOWED_PORTS): it is where a provider on the internet posts, in every
// environment. It resolves nothing. Every refusal matches ErrRefused and names the
// rule, never the URL.
func ParsePublicBase(raw string) (*url.URL, error) {
	trimmed := strings.TrimSpace(raw)
	if err := (Policy{}).CheckURL(trimmed); err != nil {
		return nil, err
	}
	parsed, _ := url.Parse(trimmed) // CheckURL refused a URL that does not parse
	if parsed.RawQuery != "" || parsed.ForceQuery || strings.Contains(trimmed, "#") || strings.HasSuffix(parsed.Host, ":") {
		return nil, refuse(ReasonBaseURL)
	}
	return parsed, nil
}
