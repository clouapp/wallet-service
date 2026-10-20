package egress

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/macrowallets/waas/tests/fakedns"
)

func TestGuard_CheckDestination_RefusesANameThatResolvesToAnInternalAddressWithOneReason(t *testing.T) {
	dns := fakedns.Start(t, map[string][]string{
		"public.example.test":   {"93.184.216.34", "2606:2800:220:1:248:1893:25c8:1946"},
		"metadata.example.test": {"169.254.169.254"},
		"private.example.test":  {"10.0.0.7"},
		"mixed.example.test":    {"93.184.216.34", "172.17.0.1"},
		"mapped.example.test":   {"::ffff:127.0.0.1"},
		"loopback.example.test": {"127.0.0.1"},
		"reserved.example.test": {"192.0.2.10"},
	})
	guard := NewGuard(Policy{}, dns.Resolver())
	ctx := context.Background()

	if err := guard.CheckDestination(ctx, "https://public.example.test/in"); err != nil {
		t.Fatalf("public name refused: %v", err)
	}
	// Whatever the range, a name refused for what it resolves to gets the same
	// sentence: the answer never tells which internal record exists.
	for _, name := range []string{"metadata", "private", "mixed", "mapped", "loopback", "reserved"} {
		assertRefusal(t, guard.CheckDestination(ctx, "https://"+name+".example.test/in"), ReasonResolved)
	}
	// A literal keeps the rule it broke.
	assertRefusal(t, guard.CheckDestination(ctx, "https://10.0.0.7/in"), ReasonPrivate)
}

func TestGuard_CheckDestination_RefusesAStaticProblemBeforeAnyLookup(t *testing.T) {
	dns := fakedns.Start(t, map[string][]string{"hooks.example.test": {"93.184.216.34"}})
	guard := NewGuard(Policy{}, dns.Resolver())

	assertRefusal(t, guard.CheckDestination(context.Background(), "http://hooks.example.test/in"), ReasonScheme)
	assertRefusal(t, guard.CheckDestination(context.Background(), "https://hooks.example.test:22/in"), ReasonPort)
	assertRefusal(t, guard.CheckDestination(context.Background(), "https://169.254.169.254/latest/"), ReasonLinkLocal)
	assertRefusal(t, guard.CheckDestination(context.Background(), "https://openbao/v1/sys/health"), ReasonHostName)
	if queries := dns.Queries(); len(queries) != 0 {
		t.Fatalf("queries = %v, want none", queries)
	}
}

func TestGuard_CheckDestination_LeavesANameThatDoesNotResolveToTheDial(t *testing.T) {
	dns := fakedns.Start(t, nil)
	guard := NewGuard(Policy{}, dns.Resolver())
	if err := guard.CheckDestination(context.Background(), "https://not-yet-deployed.example.test/in"); err != nil {
		t.Fatalf("a name without an answer was refused: %v", err)
	}

	dns.Silence()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	if err := guard.CheckDestination(ctx, "https://slow.example.test/in"); err != nil {
		t.Fatalf("a lookup that timed out was refused: %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("the lookup outlived the caller's deadline: %s", elapsed)
	}
}

func TestGuard_CheckDestination_LooksAnIPLiteralUpNever(t *testing.T) {
	dns := fakedns.Start(t, nil)
	if err := NewGuard(Policy{}, dns.Resolver()).CheckDestination(context.Background(), "https://93.184.216.34/in"); err != nil {
		t.Fatal(err)
	}
	if queries := dns.Queries(); len(queries) != 0 {
		t.Fatalf("queries = %v, want none", queries)
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

func TestGuard_DialContext_ConnectsToAnAllowedAddressOfTheName(t *testing.T) {
	ln := listen(t)
	dns := fakedns.Start(t, map[string][]string{"hooks.example.test": {"127.0.0.1"}})
	guard := NewGuard(Policy{AllowLoopback: true}, dns.Resolver())

	conn, err := guard.DialContext(context.Background(), "tcp", net.JoinHostPort("hooks.example.test", ln.port))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	_ = conn.Close()
	ln.waitAccepted(t, 1)
}

func TestGuard_DialContext_RefusesANameThatRebindsToAnInternalAddress(t *testing.T) {
	ln := listen(t)
	dns := fakedns.Start(t, map[string][]string{"hooks.example.test": {"93.184.216.34"}})
	guard := NewGuard(Policy{}, dns.Resolver())

	if err := guard.CheckDestination(context.Background(), "https://hooks.example.test/in"); err != nil {
		t.Fatalf("registration refused a public answer: %v", err)
	}
	dns.Set("hooks.example.test", "127.0.0.1")

	_, err := guard.DialContext(context.Background(), "tcp", net.JoinHostPort("hooks.example.test", ln.port))
	assertRefusal(t, err, ReasonResolved)
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("%v does not match ErrRefused", err)
	}
	time.Sleep(20 * time.Millisecond)
	if got := ln.accepted.Load(); got != 0 {
		t.Fatalf("a refused address was connected to %d times", got)
	}
}

func TestGuard_DialContext_SkipsARefusedAddressAndNeverConnectsToIt(t *testing.T) {
	ln := listen(t)
	// The private answer is refused on the socket itself, before connect; the
	// loopback one, allowed by this policy, is the one connected to.
	dns := fakedns.Start(t, map[string][]string{"hooks.example.test": {"10.1.2.3", "127.0.0.1"}})
	guard := NewGuard(Policy{AllowLoopback: true}, dns.Resolver())

	conn, err := guard.DialContext(context.Background(), "tcp4", net.JoinHostPort("hooks.example.test", ln.port))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if got := conn.RemoteAddr().String(); got != net.JoinHostPort("127.0.0.1", ln.port) {
		t.Fatalf("connected to %s", got)
	}
	_ = conn.Close()
}

// TestGuard_DialContext_GivesEachAddressItsShareOfTheTime: an address that never
// answers gets only part of the dial deadline, so the next address still connects
// in time. On macOS 127.0.0.2 is not configured and its SYNs are dropped (on Linux
// it refuses at once, which proves less but still passes). Both are loopback, so
// the resolver keeps their order.
func TestGuard_DialContext_GivesEachAddressItsShareOfTheTime(t *testing.T) {
	ln := listen(t)
	dns := fakedns.Start(t, map[string][]string{"hooks.example.test": {"127.0.0.2", "127.0.0.1"}})
	guard := NewGuard(Policy{AllowLoopback: true}, dns.Resolver())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	started := time.Now()
	conn, err := guard.DialContext(ctx, "tcp4", net.JoinHostPort("hooks.example.test", ln.port))
	if err != nil {
		t.Fatalf("dial after %s: %v", time.Since(started), err)
	}
	_ = conn.Close()
	if elapsed := time.Since(started); elapsed > 4*time.Second {
		t.Fatalf("the good address waited %s behind the silent one", elapsed)
	}
	ln.waitAccepted(t, 1)
}

func TestGuard_DialContext_RefusesAnInternalLiteralAndNameWithoutALookup(t *testing.T) {
	dns := fakedns.Start(t, nil)
	guard := NewGuard(Policy{}, dns.Resolver())
	for address, reason := range map[string]string{
		"169.254.169.254:80":          ReasonLinkLocal,
		"[::ffff:10.0.0.1]:443":       ReasonPrivate,
		"127.0.0.1:8200":              ReasonLoopback,
		"93.184.216.34:22":            ReasonPort,
		"metadata.google.internal:80": ReasonMetadata,
		"openbao:8200":                ReasonHostName,
		"0x7f000001:80":               ReasonHostName,
		"hooks.example.test:25":       ReasonPort,
	} {
		_, err := guard.DialContext(context.Background(), "tcp", address)
		assertRefusal(t, err, reason)
	}
	if queries := dns.Queries(); len(queries) != 0 {
		t.Fatalf("queries = %v, want none", queries)
	}
}

func TestGuard_DialContext_ReportsALookupFailureAsADialError(t *testing.T) {
	_, err := NewGuard(Policy{}, fakedns.Start(t, nil).Resolver()).DialContext(context.Background(), "tcp", "gone.example.test:443")
	if err == nil || errors.Is(err, ErrRefused) {
		t.Fatalf("err = %v, want a lookup failure that is not a refusal", err)
	}
	var dnsErr *net.DNSError
	if !errors.As(err, &dnsErr) {
		t.Fatalf("err = %v, want the resolver's error", err)
	}
}

func TestNewGuard_UsesTheSystemResolverByDefault(t *testing.T) {
	guard := NewGuard(Policy{}, nil)
	if guard.resolver != net.DefaultResolver || guard.dialer.Resolver != net.DefaultResolver {
		t.Fatal("a nil resolver must be the system resolver, for lookups and dials alike")
	}
	if err := guard.CheckURL("https://10.0.0.1/in"); !errors.Is(err, ErrRefused) {
		t.Fatalf("CheckURL = %v", err)
	}
}
