// Package publicurl decides whether a URL is a public https base a service on the
// internet can post to. It reads no DNS: a host name is judged as it is written.
package publicurl

import (
	"errors"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// ErrNotPublicBase is a URL a service on the internet cannot post to: not https,
// with credentials, a query or a fragment, an invalid port, or a host that is
// localhost or an internal address.
var ErrNotPublicBase = errors.New("publicurl: not a public https base URL")

// ParseBase returns raw, trimmed and parsed, when it is a public https origin with
// an optional path prefix: no user name or password, no query or fragment (an empty
// one included), a port from 1 to 65535 when one is given, and a host that is a
// name or a public IP. Anything else is ErrNotPublicBase.
func ParseBase(raw string) (*url.URL, error) {
	trimmed := strings.TrimSpace(raw)
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery ||
		parsed.Fragment != "" || strings.Contains(trimmed, "#") || !publicHost(parsed) {
		return nil, ErrNotPublicBase
	}
	return parsed, nil
}

// publicHost reports whether the URL names a host a provider can post to: a name or
// a public IP, with a valid port when one is given; never localhost, a private,
// loopback, link-local (the cloud metadata address included), unspecified or shared
// address.
func publicHost(parsed *url.URL) bool {
	host := strings.ToLower(parsed.Hostname())
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return false
	}
	if port := parsed.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return false
		}
	} else if strings.HasSuffix(parsed.Host, ":") {
		return false
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return true
	}
	return !internalAddress(ip)
}

// internalAddress reports an IP a provider on the internet cannot reach: loopback,
// private, link-local (the cloud metadata address), unspecified, multicast or shared.
func internalAddress(ip netip.Addr) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || sharedAddressSpace.Contains(ip.Unmap())
}

// sharedAddressSpace is 100.64.0.0/10 (carrier-grade NAT), reachable only inside a network.
var sharedAddressSpace = netip.MustParsePrefix("100.64.0.0/10")
