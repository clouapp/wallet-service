// Package clientip puts the address a request came from in the one shape a
// provider records it in.
package clientip

import (
	"net/netip"
	"strings"
)

// Canonical canonicalises an address, or answers "" for one that does not
// parse. An IPv6 zone names an interface on this host, which means nothing to
// a provider, so it is dropped; an IPv4 address seen mapped into IPv6 (a
// dual-stack listener) is returned as the IPv4 address it is.
func Canonical(raw string) string {
	addr, err := netip.ParseAddr(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return addr.WithZone("").Unmap().String()
}
