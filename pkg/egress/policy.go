package egress

import (
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// Policy says which destinations an outbound URL may name. The zero value is the
// policy of a webhook receiver: https only, to a public host. Each Allow field
// relaxes one rule; unspecified, multicast, broadcast and reserved addresses, host
// names in a numeric form (2130706433, 0x7f000001, 0177.0.0.1, 127.1) and the ports
// of SSH (22) and SMTP (25) are refused by every policy.
type Policy struct {
	// AllowHTTP accepts http:// besides https:// (a dev or test receiver).
	AllowHTTP bool
	// AllowLoopback accepts 127.0.0.0/8, ::1 and localhost names; a loopback
	// address literal may then use http:// as well (a test receiver).
	AllowLoopback bool
	// AllowPrivate accepts RFC 1918, fc00::/7 and 100.64.0.0/10 addresses,
	// single-label names and the internal suffixes (.internal, .local, ...): a node
	// on the operator's own network.
	AllowPrivate bool
	// AllowLinkLocal accepts 169.254.0.0/16, fe80::/10 and the cloud metadata names
	// and addresses.
	AllowLinkLocal bool
	// AllowUserinfo accepts user:password@ in the URL (a node behind basic auth).
	AllowUserinfo bool
	// AllowedPorts, when not empty, are the only ports a destination may use (a URL
	// without a port uses 443 for https and 80 for http).
	AllowedPorts []int
}

// RPCPolicy is the policy of an RPC node URL that an operator or a platform admin
// writes: operators run their own nodes, so http, credentials, loopback, private
// networks and internal names are allowed; link-local and cloud metadata
// destinations only with allowLinkLocal (RPC_ALLOW_LINK_LOCAL).
func RPCPolicy(allowLinkLocal bool) Policy {
	return Policy{
		AllowHTTP:      true,
		AllowLoopback:  true,
		AllowPrivate:   true,
		AllowLinkLocal: allowLinkLocal,
		AllowUserinfo:  true,
	}
}

// CheckURL refuses rawURL when it is not an absolute http or https URL with a valid
// host and port, or when it names a destination the policy does not allow. It
// resolves nothing: Guard.CheckDestination and Guard.DialContext look names up.
func (p Policy) CheckURL(rawURL string) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return refuse(ReasonInvalidURL)
	}
	return p.checkParsedURL(parsed)
}

// checkParsedURL is CheckURL on a URL already parsed.
func (p Policy) checkParsedURL(parsed *url.URL) error {
	if parsed.Opaque != "" || parsed.Host == "" || !validPort(parsed.Port()) {
		return refuse(ReasonInvalidURL)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "https" && scheme != "http" {
		return refuse(ReasonInvalidURL)
	}
	if parsed.User != nil && !p.AllowUserinfo {
		return refuse(ReasonUserinfo)
	}
	addr, literal, err := p.checkHost(parsed.Hostname())
	if err != nil {
		return err
	}
	if err := p.checkPort(urlPort(scheme, parsed.Port())); err != nil {
		return err
	}
	testReceiver := p.AllowLoopback && literal && addr.Unmap().IsLoopback()
	if scheme == "http" && !p.AllowHTTP && !testReceiver {
		return refuse(ReasonScheme)
	}
	return nil
}

// CheckAddr refuses an address the policy does not allow. An IPv4-mapped IPv6
// address is checked as its IPv4 address, and so is the IPv4 address a NAT64
// (64:ff9b::/96) or 6to4 (2002::/16) address embeds.
func (p Policy) CheckAddr(addr netip.Addr) error {
	if !addr.IsValid() {
		return refuse(ReasonInvalidURL)
	}
	addr = addr.Unmap().WithZone("")
	if embedded, ok := embeddedIPv4(addr); ok {
		return p.CheckAddr(embedded)
	}
	switch {
	case addr.IsUnspecified():
		return refuse(ReasonReserved)
	case addr.IsLoopback():
		return allowIf(p.AllowLoopback, ReasonLoopback)
	case metadataAddrs[addr] || addr.IsLinkLocalUnicast():
		return allowIf(p.AllowLinkLocal, ReasonLinkLocal)
	case addr.IsMulticast() || inAny(addr, reservedPrefixes):
		return refuse(ReasonReserved)
	case addr.IsPrivate() || sharedAddressSpace.Contains(addr):
		return allowIf(p.AllowPrivate, ReasonPrivate)
	}
	return nil
}

// checkHost checks a URL host: an address literal by CheckAddr, a name by the name
// rules. It reports the literal it parsed.
func (p Policy) checkHost(host string) (netip.Addr, bool, error) {
	host = normalizeHost(host)
	if host == "" {
		return netip.Addr{}, false, refuse(ReasonInvalidURL)
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		if addr.Zone() != "" {
			return netip.Addr{}, false, refuse(ReasonInvalidURL)
		}
		return addr, true, p.CheckAddr(addr)
	}
	return netip.Addr{}, false, p.checkName(host)
}

// checkName refuses a host name that is not a plain DNS name, that ends in a
// numeric label (what inet_aton reads as an address: 2130706433, 0x7f000001,
// 0177.0.0.1, 127.1), that names a metadata service or loopback, or that is a
// single label or an internal name while private destinations are refused.
func (p Policy) checkName(name string) error {
	if !plainHostName(name) {
		return refuse(ReasonHostName)
	}
	labels := strings.Split(name, ".")
	if last := labels[len(labels)-1]; last[0] >= '0' && last[0] <= '9' {
		return refuse(ReasonHostName)
	}
	switch {
	case metadataNames[name]:
		return allowIf(p.AllowLinkLocal, ReasonMetadata)
	case name == "localhost" || strings.HasSuffix(name, ".localhost"):
		return allowIf(p.AllowLoopback, ReasonLoopback)
	case len(labels) == 1 || internalName(name):
		return allowIf(p.AllowPrivate, ReasonHostName)
	}
	return nil
}

func allowIf(allowed bool, reason string) error {
	if allowed {
		return nil
	}
	return refuse(reason)
}

// normalizeHost lowers a host and drops the one trailing dot of a fully qualified name.
func normalizeHost(host string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
}

// plainHostName reports a name of ASCII letters, digits, hyphens and underscores in
// non-empty dot-separated labels. A non-ASCII name must be sent in its punycode form.
func plainHostName(name string) bool {
	for _, label := range strings.Split(name, ".") {
		if label == "" {
			return false
		}
		for i := 0; i < len(label); i++ {
			if !hostNameByte(label[i]) {
				return false
			}
		}
	}
	return true
}

func hostNameByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_'
}

// refusedPorts are the ports no destination may use: SSH and SMTP, which a signed
// POST could still talk to.
var refusedPorts = map[int]bool{22: true, 25: true}

// checkPort refuses a refused port, or one outside AllowedPorts when it is set.
func (p Policy) checkPort(port int) error {
	if refusedPorts[port] {
		return refuse(ReasonPort)
	}
	if len(p.AllowedPorts) == 0 {
		return nil
	}
	for _, allowed := range p.AllowedPorts {
		if port == allowed {
			return nil
		}
	}
	return refuse(ReasonPort)
}

// urlPort is the port a URL connects to: its own, or its scheme's default. The
// port text was validated already.
func urlPort(scheme, port string) int {
	if port == "" {
		if scheme == "http" {
			return 80
		}
		return 443
	}
	n, _ := strconv.Atoi(port)
	return n
}

func validPort(port string) bool {
	if port == "" {
		return true
	}
	n, err := strconv.Atoi(port)
	return err == nil && n >= 1 && n <= 65535
}

// internalSuffixes are names only a private resolver answers: the reserved
// .internal (ICANN), .local (mDNS), .home.arpa (RFC 8375), a Kubernetes .svc and
// the suffixes private networks commonly use.
var internalSuffixes = []string{"internal", "local", "localdomain", "home.arpa", "lan", "intranet", "corp", "svc"}

func internalName(name string) bool {
	for _, suffix := range internalSuffixes {
		if name == suffix || strings.HasSuffix(name, "."+suffix) {
			return true
		}
	}
	return false
}

// metadataNames are the cloud metadata services reachable by name.
var metadataNames = map[string]bool{
	"metadata":                     true,
	"metadata.google.internal":     true,
	"metadata.goog":                true,
	"instance-data":                true,
	"instance-data.ec2.internal":   true,
	"metadata.platformequinix.com": true,
	"metadata.packet.net":          true,
	"metadata.tencentyun.com":      true,
}

// metadataAddrs are the cloud metadata addresses outside 169.254.0.0/16, which is
// link-local as a whole: AWS IMDS and EKS Pod Identity over IPv6 (in fc00::/7),
// Alibaba Cloud (in 100.64.0.0/10) and the Azure wire server (a public address).
var metadataAddrs = map[netip.Addr]bool{
	netip.MustParseAddr("fd00:ec2::254"):   true,
	netip.MustParseAddr("fd00:ec2::23"):    true,
	netip.MustParseAddr("100.100.100.200"): true,
	netip.MustParseAddr("168.63.129.16"):   true,
}

// sharedAddressSpace is the carrier-grade NAT range (RFC 6598), which IsPrivate
// does not cover.
var sharedAddressSpace = netip.MustParsePrefix("100.64.0.0/10")

// reservedPrefixes are the special-purpose ranges no public receiver is on, among
// them the transition relays that reach another network: the 6to4 relay anycast
// (192.88.99.0/24), Teredo (2001::/32) and local-use NAT64 (64:ff9b:1::/48).
var reservedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/32"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("fec0::/10"),
}

var (
	nat64Prefix = netip.MustParsePrefix("64:ff9b::/96")
	sixToFour   = netip.MustParsePrefix("2002::/16")
)

// embeddedIPv4 is the IPv4 address a NAT64 or 6to4 address carries.
func embeddedIPv4(addr netip.Addr) (netip.Addr, bool) {
	if !addr.Is6() {
		return netip.Addr{}, false
	}
	b := addr.As16()
	switch {
	case nat64Prefix.Contains(addr):
		return netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}), true
	case sixToFour.Contains(addr):
		return netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]}), true
	}
	return netip.Addr{}, false
}

func inAny(addr netip.Addr, prefixes []netip.Prefix) bool {
	for _, prefix := range prefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}
