package middleware

import (
	"fmt"
	"net/netip"
	"strings"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/facades"
)

// defaultTrustedProxies is what http.trusted_proxies falls back to when the
// key is absent (config/http_config.go sets the same value).
const defaultTrustedProxies = "127.0.0.1/32,::1/128"

// ClientIP is the address S3.4.6 compares with an API token's ip_cidr, and the
// address the rate limits count. It starts at the connection's RemoteAddr and
// reads X-Forwarded-For only when that peer is a trusted proxy
// (http.trusted_proxies). See resolveClientIP.
func ClientIP(ctx http.Context) string {
	if ctx == nil || ctx.Request() == nil {
		return ""
	}
	origin := ctx.Request().Origin()
	if origin == nil {
		return ""
	}
	// A malformed entry leaves the valid ones trusted: fewer trusted proxies can
	// only attribute a request to the proxy, never to a forged address.
	// Boot refuses a malformed entry (bootstrap.checkBootConfig).
	trusted, _ := ParseTrustedProxies(facades.Config().GetString("http.trusted_proxies", defaultTrustedProxies))
	return resolveClientIP(origin.RemoteAddr, origin.Header.Values("X-Forwarded-For"), trusted)
}

// ParseTrustedProxies reads a comma-separated list of CIDRs or bare addresses.
// A bare address is a /32 or /128. A malformed entry is reported and the valid
// ones are still returned.
func ParseTrustedProxies(list string) ([]netip.Prefix, error) {
	var prefixes []netip.Prefix
	var bad []string
	for _, entry := range strings.Split(list, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if prefix, err := netip.ParsePrefix(entry); err == nil {
			prefixes = append(prefixes, prefix.Masked())
			continue
		}
		if addr, err := netip.ParseAddr(entry); err == nil {
			addr = addr.Unmap()
			prefixes = append(prefixes, netip.PrefixFrom(addr, addr.BitLen()))
			continue
		}
		bad = append(bad, entry)
	}
	if len(bad) > 0 {
		return prefixes, fmt.Errorf("invalid trusted proxy entries: %s", strings.Join(bad, ", "))
	}
	return prefixes, nil
}

// resolveClientIP returns the first address that is not a trusted proxy, walking
// the forwarding chain from the peer back toward the origin: the peer first,
// then X-Forwarded-For right to left. Only a trusted peer may speak for the
// ones before it, so a client that talks to the app directly cannot choose its
// own address, and an entry a trusted proxy appended always wins over what the
// client wrote to its left. RemoteAddr is read with or without a port (Lambda
// passes the bare source IP). Malformed entries are skipped. An unparsable
// RemoteAddr yields "".
func resolveClientIP(remoteAddr string, forwarded []string, trusted []netip.Prefix) string {
	peer, ok := parseAddr(remoteAddr)
	if !ok {
		return ""
	}
	if !isTrusted(peer, trusted) {
		return peer.String()
	}
	var hops []string
	for _, value := range forwarded {
		hops = append(hops, strings.Split(value, ",")...)
	}
	for i := len(hops) - 1; i >= 0; i-- {
		hop, ok := parseAddr(hops[i])
		if !ok || isTrusted(hop, trusted) {
			continue
		}
		return hop.String()
	}
	return peer.String()
}

func parseAddr(text string) (netip.Addr, bool) {
	text = strings.TrimSpace(text)
	if addrPort, err := netip.ParseAddrPort(text); err == nil {
		return addrPort.Addr().Unmap(), true
	}
	if addr, err := netip.ParseAddr(text); err == nil {
		return addr.Unmap(), true
	}
	return netip.Addr{}, false
}

func isTrusted(addr netip.Addr, trusted []netip.Prefix) bool {
	for _, prefix := range trusted {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}
