package egress

import (
	"context"
	"net"
	"net/netip"
	"time"
)

// Resolver looks a host name up; *net.Resolver is one.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

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
	resolver Resolver
	dialer   *net.Dialer
}

// NewGuard builds the guard of policy; a nil resolver is the system's
// (net.DefaultResolver).
func NewGuard(policy Policy, resolver Resolver) *Guard {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	return &Guard{
		policy:   policy,
		resolver: resolver,
		dialer:   &net.Dialer{Timeout: dialTimeout, KeepAlive: dialKeepAlive},
	}
}

// CheckURL is the policy's check of rawURL; it resolves nothing.
func (g *Guard) CheckURL(rawURL string) error {
	return g.policy.CheckURL(rawURL)
}

// CheckDestination is CheckURL, then a lookup of a host name: every address the name
// resolves to now must be allowed. A name that does not resolve within the caller's
// deadline or 2 s is not refused here (a receiver not deployed yet, a resolver that
// is down): DialContext checks the addresses of every connection.
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
	return g.checkAddrs(addrs)
}

// DialContext opens a connection to address (host:port) only through addresses the
// policy allows: a literal is checked as it is; a name is checked by the name rules,
// resolved here, and refused when any address it resolves to is refused. The
// connection is made to a checked address itself, so no later lookup can move it.
// It is the DialContext of the transport a guarded client sends through.
func (g *Guard) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addrs, err := g.resolve(ctx, network, host)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, addr := range addrs {
		conn, err := g.dialer.DialContext(ctx, network, net.JoinHostPort(addr.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// resolve returns the checked addresses of host.
func (g *Guard) resolve(ctx context.Context, network, host string) ([]netip.Addr, error) {
	addr, literal, err := g.policy.checkHost(host)
	if err != nil {
		return nil, err
	}
	if literal {
		return []netip.Addr{addr}, nil
	}
	name := normalizeHost(host)
	addrs, err := g.resolver.LookupNetIP(ctx, lookupNetwork(network), name)
	if err != nil {
		return nil, err
	}
	if len(addrs) == 0 {
		return nil, &net.DNSError{Err: "no addresses", Name: name, IsNotFound: true}
	}
	if err := g.checkAddrs(addrs); err != nil {
		return nil, err
	}
	return addrs, nil
}

func (g *Guard) checkAddrs(addrs []netip.Addr) error {
	for _, addr := range addrs {
		if err := g.policy.CheckAddr(addr); err != nil {
			return err
		}
	}
	return nil
}

// lookupNetwork is the address family a dial network asks for.
func lookupNetwork(network string) string {
	switch network {
	case "tcp4", "udp4":
		return "ip4"
	case "tcp6", "udp6":
		return "ip6"
	}
	return "ip"
}
