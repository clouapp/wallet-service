package egress

import (
	"context"
	"net"
	"net/netip"
	"syscall"
	"time"
)

const (
	// lookupBudget bounds the lookup of CheckDestination, which runs inside a request.
	lookupBudget = 2 * time.Second
	// dialTimeout and dialKeepAlive are the ones of http.DefaultTransport.
	dialTimeout   = 30 * time.Second
	dialKeepAlive = 30 * time.Second
)

// Guard applies a Policy to a URL and to every connection made for one.
type Guard struct {
	policy   Policy
	resolver *net.Resolver
	dialer   *net.Dialer
}

// NewGuard builds the guard of policy over resolver, which answers both its lookups
// and the names it dials; nil is the system's (net.DefaultResolver).
func NewGuard(policy Policy, resolver *net.Resolver) *Guard {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	g := &Guard{policy: policy, resolver: resolver}
	g.dialer = &net.Dialer{Timeout: dialTimeout, KeepAlive: dialKeepAlive, Resolver: resolver, ControlContext: g.control}
	return g
}

// CheckURL is the policy's check of rawURL; it resolves nothing.
func (g *Guard) CheckURL(rawURL string) error {
	return g.policy.CheckURL(rawURL)
}

// CheckDestination is CheckURL, then a lookup of a host name: every address the name
// resolves to now must be allowed. A name refused for what it resolves to always
// gets ReasonResolved, whatever the range, so the answer does not tell an internal
// record from another. A name that does not resolve within the caller's deadline or
// 2 s is not refused here (a receiver not deployed yet, a resolver that is down):
// DialContext checks every connection.
func (g *Guard) CheckDestination(ctx context.Context, rawURL string) error {
	if err := g.CheckURL(rawURL); err != nil {
		return err
	}
	host := normalizeHost(Host(rawURL))
	if _, err := netip.ParseAddr(host); err == nil {
		return nil
	}
	lookupCtx, cancel := context.WithTimeout(ctx, lookupBudget)
	defer cancel()
	addrs, err := g.resolver.LookupNetIP(lookupCtx, "ip", host)
	if err != nil {
		return nil
	}
	for _, addr := range addrs {
		if g.policy.CheckAddr(addr) != nil {
			return refuse(ReasonResolved)
		}
	}
	return nil
}

// DialContext opens a connection to address (host:port) only to an address the
// policy allows. The port and the host are checked first: a literal by its rule, a
// name by the name rules. Then Go's dialer resolves the name, tries its addresses
// with its own deadline share per address and IPv6/IPv4 fallback, and before every
// connect calls control with the socket's own address, which refuses one the policy
// does not allow (ReasonResolved). So no refused address is ever connected to,
// whatever the name resolves to at that moment (DNS rebinding included), and one
// refused or silent address does not take the whole deadline. It is the
// DialContext of the transport a guarded client sends through.
func (g *Guard) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if !validPort(port) {
		return nil, refuse(ReasonInvalidURL)
	}
	if err := g.policy.checkPort(urlPort("", port)); err != nil {
		return nil, err
	}
	if _, _, err := g.policy.checkHost(host); err != nil {
		return nil, err
	}
	return g.dialer.DialContext(ctx, network, address)
}

// control refuses, right before connect, a socket address the policy does not
// allow. A literal host was checked with its own rule already, and the port before
// the dial, so a refusal here is always about what a name resolved to.
func (g *Guard) control(_ context.Context, _, address string, _ syscall.RawConn) error {
	socket, err := netip.ParseAddrPort(address)
	if err != nil || g.policy.CheckAddr(socket.Addr()) != nil {
		return refuse(ReasonResolved)
	}
	return nil
}
