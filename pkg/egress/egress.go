// Package egress decides which destinations an outbound request may reach. A URL
// that a customer or a platform admin stores (a webhook receiver, an RPC node) is
// checked when it is written (Policy.CheckURL, Guard.CheckDestination) and, for a
// webhook, again right before every connection (Guard.DialContext): the name is
// resolved there and the connection goes to the address that was checked, so a
// record that points elsewhere after the check (DNS rebinding) reaches nothing the
// policy refuses.
package egress

import (
	"errors"
	"net/url"
	"strings"
)

// ErrRefused matches every refusal of a Policy or a Guard (errors.Is).
var ErrRefused = errors.New("egress: destination refused")

// Refusal is a destination a Policy does not allow. Reason is one of the Reason
// sentences: it names the rule, never the URL, so it may be shown to the client
// that sent the URL.
type Refusal struct {
	Reason string
}

func (r *Refusal) Error() string {
	if r == nil {
		return ErrRefused.Error()
	}
	return r.Reason
}

// Is makes every refusal match ErrRefused.
func (r *Refusal) Is(target error) bool {
	return target == ErrRefused
}

// The sentences a Refusal carries.
const (
	ReasonInvalidURL = "the url must be an absolute http or https url with a valid host and port"
	ReasonScheme     = "the url must use https"
	ReasonUserinfo   = "the url must not carry a user name or password"
	ReasonHostName   = "the url host must be a public domain name or address, not a single-label, internal or numeric-form name"
	ReasonMetadata   = "the url host must not name a cloud metadata service"
	ReasonLoopback   = "the url host must not be or resolve to a loopback address"
	ReasonPrivate    = "the url host must not be or resolve to a private or shared address"
	ReasonLinkLocal  = "the url host must not be or resolve to a link-local or cloud metadata address"
	ReasonReserved   = "the url host must not be or resolve to an unspecified, multicast, broadcast or reserved address"
	ReasonPort       = "the url port is not allowed"
	ReasonResolved   = "the url host resolves to an address that is not allowed"
)

func refuse(reason string) error {
	return &Refusal{Reason: reason}
}

// Host is the host name or address of rawURL, in lower case and without the user
// information, the port, the path or the query: what a log line may name. It is
// empty when rawURL does not parse.
func Host(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}
