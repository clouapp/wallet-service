package fakedns

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"
)

func TestResolver_AnswersTheTableAndFollowsAChange(t *testing.T) {
	server := Start(t, map[string][]string{"hooks.example.test": {"93.184.216.34", "2606:2800:220:1::1"}})
	resolver := server.Resolver()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	addrs, err := resolver.LookupNetIP(ctx, "ip", "hooks.example.test")
	if err != nil || len(addrs) != 2 {
		t.Fatalf("lookup = %v, %v", addrs, err)
	}
	server.Set("hooks.example.test", "127.0.0.1")
	addrs, err = resolver.LookupNetIP(ctx, "ip4", "hooks.example.test")
	if err != nil || len(addrs) != 1 || addrs[0] != netip.MustParseAddr("127.0.0.1") {
		t.Fatalf("after the change = %v, %v", addrs, err)
	}
	var dnsErr *net.DNSError
	if _, err := resolver.LookupNetIP(ctx, "ip", "unknown.example.test"); !errors.As(err, &dnsErr) || !dnsErr.IsNotFound {
		t.Fatalf("unknown name = %v, want not found", err)
	}
}

func TestResolver_OfASilentServerTimesOutWithTheCaller(t *testing.T) {
	server := Start(t, map[string][]string{"hooks.example.test": {"93.184.216.34"}})
	server.Silence()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := server.Resolver().LookupNetIP(ctx, "ip", "hooks.example.test"); err == nil {
		t.Fatal("a silent server answered")
	}
	if time.Since(started) > 2*time.Second {
		t.Fatal("the lookup outlived the caller's deadline")
	}
}
