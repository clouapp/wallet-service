package egress

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeResolver answers each host from a table; a host it does not know fails like
// a name that does not exist. Every lookup is recorded.
type fakeResolver struct {
	mu      sync.Mutex
	answers map[string][]netip.Addr
	block   bool
	lookups []string
}

func resolving(answers map[string][]string) *fakeResolver {
	r := &fakeResolver{answers: map[string][]netip.Addr{}}
	for host, addrs := range answers {
		for _, addr := range addrs {
			r.answers[host] = append(r.answers[host], netip.MustParseAddr(addr))
		}
	}
	return r
}

func (r *fakeResolver) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	r.mu.Lock()
	r.lookups = append(r.lookups, network+" "+host)
	block := r.block
	addrs, ok := r.answers[host]
	r.mu.Unlock()
	if block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	return addrs, nil
}

// rebind makes host answer addrs from now on, as a DNS record changed after a check.
func (r *fakeResolver) rebind(host string, addrs ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.answers[host] = nil
	for _, addr := range addrs {
		r.answers[host] = append(r.answers[host], netip.MustParseAddr(addr))
	}
}

func (r *fakeResolver) calls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.lookups...)
}

func TestGuard_CheckDestination_RefusesANameThatResolvesToAnInternalAddress(t *testing.T) {
	resolver := resolving(map[string][]string{
		"public.example.com":   {"93.184.216.34", "2606:2800:220:1:248:1893:25c8:1946"},
		"metadata.example.com": {"169.254.169.254"},
		"private.example.com":  {"10.0.0.7"},
		"mixed.example.com":    {"93.184.216.34", "172.17.0.1"},
		"mapped.example.com":   {"::ffff:127.0.0.1"},
		"loopback.example.com": {"127.0.0.1"},
	})
	guard := NewGuard(Policy{}, resolver)
	ctx := context.Background()

	if err := guard.CheckDestination(ctx, "https://public.example.com/in"); err != nil {
		t.Fatalf("public name refused: %v", err)
	}
	assertRefusal(t, guard.CheckDestination(ctx, "https://metadata.example.com/latest/"), ReasonLinkLocal)
	assertRefusal(t, guard.CheckDestination(ctx, "https://private.example.com/in"), ReasonPrivate)
	assertRefusal(t, guard.CheckDestination(ctx, "https://mixed.example.com/in"), ReasonPrivate)
	assertRefusal(t, guard.CheckDestination(ctx, "https://mapped.example.com/in"), ReasonLoopback)
	assertRefusal(t, guard.CheckDestination(ctx, "https://loopback.example.com/in"), ReasonLoopback)
}

func TestGuard_CheckDestination_RefusesAStaticProblemBeforeAnyLookup(t *testing.T) {
	resolver := resolving(map[string][]string{"hooks.example.com": {"93.184.216.34"}})
	guard := NewGuard(Policy{}, resolver)

	assertRefusal(t, guard.CheckDestination(context.Background(), "http://hooks.example.com/in"), ReasonScheme)
	assertRefusal(t, guard.CheckDestination(context.Background(), "https://169.254.169.254/latest/"), ReasonLinkLocal)
	assertRefusal(t, guard.CheckDestination(context.Background(), "https://openbao/v1/sys/health"), ReasonHostName)
	if calls := resolver.calls(); len(calls) != 0 {
		t.Fatalf("lookups = %v, want none", calls)
	}
}

func TestGuard_CheckDestination_LeavesANameThatDoesNotResolveToTheDial(t *testing.T) {
	guard := NewGuard(Policy{}, resolving(nil))
	if err := guard.CheckDestination(context.Background(), "https://not-yet-deployed.example.com/in"); err != nil {
		t.Fatalf("a name without an answer was refused: %v", err)
	}

	hanging := resolving(nil)
	hanging.block = true
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	if err := NewGuard(Policy{}, hanging).CheckDestination(ctx, "https://slow.example.com/in"); err != nil {
		t.Fatalf("a lookup that timed out was refused: %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("the lookup outlived the caller's deadline: %s", elapsed)
	}
}

func TestGuard_CheckDestination_LooksAnIPLiteralUpNever(t *testing.T) {
	resolver := resolving(nil)
	if err := NewGuard(Policy{}, resolver).CheckDestination(context.Background(), "https://93.184.216.34/in"); err != nil {
		t.Fatal(err)
	}
	if calls := resolver.calls(); len(calls) != 0 {
		t.Fatalf("lookups = %v, want none", calls)
	}
}

// listener accepts and counts connections on a loopback port.
type listener struct {
	port     string
	accepted atomic.Int32
}

func listen(t *testing.T) *listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	l := &listener{}
	_, l.port, _ = net.SplitHostPort(ln.Addr().String())
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			l.accepted.Add(1)
			_ = conn.Close()
		}
	}()
	return l
}

func (l *listener) waitAccepted(t *testing.T, want int32) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for l.accepted.Load() < want && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := l.accepted.Load(); got != want {
		t.Fatalf("accepted = %d, want %d", got, want)
	}
}

func TestGuard_DialContext_ConnectsToTheAddressItChecked(t *testing.T) {
	ln := listen(t)
	resolver := resolving(map[string][]string{"hooks.example.test": {"127.0.0.1"}})
	guard := NewGuard(Policy{AllowLoopback: true}, resolver)

	conn, err := guard.DialContext(context.Background(), "tcp", net.JoinHostPort("hooks.example.test", ln.port))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	_ = conn.Close()
	ln.waitAccepted(t, 1)
	if calls := resolver.calls(); len(calls) != 1 || calls[0] != "ip hooks.example.test" {
		t.Fatalf("lookups = %v, want one of hooks.example.test", calls)
	}
}

func TestGuard_DialContext_RefusesANameThatRebindsToAnInternalAddress(t *testing.T) {
	ln := listen(t)
	resolver := resolving(map[string][]string{"hooks.example.test": {"93.184.216.34"}})
	guard := NewGuard(Policy{}, resolver)

	if err := guard.CheckDestination(context.Background(), "https://hooks.example.test/in"); err != nil {
		t.Fatalf("registration refused a public answer: %v", err)
	}
	resolver.rebind("hooks.example.test", "127.0.0.1")

	_, err := guard.DialContext(context.Background(), "tcp", net.JoinHostPort("hooks.example.test", ln.port))
	assertRefusal(t, err, ReasonLoopback)
	time.Sleep(20 * time.Millisecond)
	if got := ln.accepted.Load(); got != 0 {
		t.Fatalf("a refused destination was connected to %d times", got)
	}

	resolver.rebind("hooks.example.test", "93.184.216.34", "10.1.2.3")
	_, err = guard.DialContext(context.Background(), "tcp", "hooks.example.test:443")
	assertRefusal(t, err, ReasonPrivate)
}

func TestGuard_DialContext_RefusesAnInternalLiteralAndNameWithoutALookup(t *testing.T) {
	resolver := resolving(nil)
	guard := NewGuard(Policy{}, resolver)
	for address, reason := range map[string]string{
		"169.254.169.254:80":          ReasonLinkLocal,
		"[::ffff:10.0.0.1]:443":       ReasonPrivate,
		"127.0.0.1:8200":              ReasonLoopback,
		"metadata.google.internal:80": ReasonMetadata,
		"openbao:8200":                ReasonHostName,
		"0x7f000001:80":               ReasonHostName,
	} {
		_, err := guard.DialContext(context.Background(), "tcp", address)
		assertRefusal(t, err, reason)
	}
	if calls := resolver.calls(); len(calls) != 0 {
		t.Fatalf("lookups = %v, want none", calls)
	}
}

func TestGuard_DialContext_AsksTheFamilyTheNetworkNames(t *testing.T) {
	resolver := resolving(map[string][]string{"hooks.example.test": {"93.184.216.34"}})
	guard := NewGuard(Policy{}, resolver)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _ = guard.DialContext(ctx, "tcp4", "hooks.example.test:443")
	_, _ = guard.DialContext(ctx, "tcp6", "hooks.example.test:443")
	calls := resolver.calls()
	if len(calls) != 2 || calls[0] != "ip4 hooks.example.test" || calls[1] != "ip6 hooks.example.test" {
		t.Fatalf("lookups = %v", calls)
	}
}

func TestGuard_DialContext_ReportsALookupFailureAsADialError(t *testing.T) {
	_, err := NewGuard(Policy{}, resolving(nil)).DialContext(context.Background(), "tcp", "gone.example.com:443")
	if err == nil || errors.Is(err, ErrRefused) {
		t.Fatalf("err = %v, want a lookup failure that is not a refusal", err)
	}
	var dnsErr *net.DNSError
	if !errors.As(err, &dnsErr) {
		t.Fatalf("err = %v, want the resolver's error", err)
	}
}

func TestGuard_DialContext_TriesTheNextCheckedAddressWhenOneDoesNotAnswer(t *testing.T) {
	ln := listen(t)
	// The listener is on 127.0.0.1 only, so ::1 refuses the connection (or has no
	// route) and the second answer is tried.
	resolver := resolving(map[string][]string{"hooks.example.test": {"::1", "127.0.0.1"}})
	guard := NewGuard(Policy{AllowLoopback: true}, resolver)
	conn, err := guard.DialContext(context.Background(), "tcp", net.JoinHostPort("hooks.example.test", ln.port))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	_ = conn.Close()
	ln.waitAccepted(t, 1)
}

func TestNewGuard_UsesTheSystemResolverByDefault(t *testing.T) {
	guard := NewGuard(Policy{}, nil)
	if guard.resolver != net.DefaultResolver {
		t.Fatal("a nil resolver must be the system resolver")
	}
	if err := guard.CheckURL("https://10.0.0.1/in"); !errors.Is(err, ErrRefused) {
		t.Fatalf("CheckURL = %v", err)
	}
}
