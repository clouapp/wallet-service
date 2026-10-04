package policies

import (
	"net/netip"
	"strings"
)

// APITokenIPAllows reports whether clientIP may use a token that stored
// ip_cidr. A blank allowlist keeps today's access: S3.4.6 does not take it
// away. A list is one CIDR or address, or several separated by commas. The
// client must sit in one of them. A value that is not a CIDR denies, and so
// does a client address that cannot be parsed.
func APITokenIPAllows(stored, clientIP string) bool {
	prefixes, ok := apiTokenIPPrefixes(stored)
	if !ok {
		return false
	}
	if len(prefixes) == 0 {
		return true
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(clientIP))
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	for _, prefix := range prefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// ValidAPITokenIPCIDR reports whether a write may store ip_cidr. Blank is
// valid. Anything else must be the same allowlist APITokenIPAllows reads.
func ValidAPITokenIPCIDR(stored string) bool {
	_, ok := apiTokenIPPrefixes(stored)
	return ok
}

func apiTokenIPPrefixes(stored string) ([]netip.Prefix, bool) {
	stored = strings.TrimSpace(stored)
	if stored == "" {
		return nil, true
	}
	parts := strings.Split(stored, ",")
	prefixes := make([]netip.Prefix, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, false
		}
		prefix, ok := apiTokenIPPrefix(part)
		if !ok {
			return nil, false
		}
		prefixes = append(prefixes, prefix)
	}
	return prefixes, true
}

func apiTokenIPPrefix(part string) (netip.Prefix, bool) {
	if prefix, err := netip.ParsePrefix(part); err == nil {
		return prefix.Masked(), true
	}
	addr, err := netip.ParseAddr(part)
	if err != nil {
		return netip.Prefix{}, false
	}
	addr = addr.Unmap()
	return netip.PrefixFrom(addr, addr.BitLen()), true
}
