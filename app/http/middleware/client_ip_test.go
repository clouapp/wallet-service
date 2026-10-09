package middleware

import (
	"net/netip"
	"testing"
)

func TestResolve_ClientIP(t *testing.T) {
	loopback := mustPrefixes(t, "127.0.0.1/32,::1/128")

	tests := []struct {
		name       string
		remoteAddr string
		forwarded  []string
		trusted    []netip.Prefix
		want       string
	}{
		{
			name:       "direct client forging X-Forwarded-For keeps its own address",
			remoteAddr: "203.0.113.7:51000",
			forwarded:  []string{"6.6.6.6"},
			trusted:    loopback,
			want:       "203.0.113.7",
		},
		{
			name:       "direct client forging a chain that ends at a trusted address keeps its own address",
			remoteAddr: "203.0.113.7:51000",
			forwarded:  []string{"6.6.6.6, 127.0.0.1"},
			trusted:    loopback,
			want:       "203.0.113.7",
		},
		{
			name:       "nginx on loopback forwarding the real client",
			remoteAddr: "127.0.0.1:41000",
			forwarded:  []string{"198.51.100.20"},
			trusted:    loopback,
			want:       "198.51.100.20",
		},
		{
			name:       "nginx appending to a chain the client started",
			remoteAddr: "127.0.0.1:41000",
			forwarded:  []string{"6.6.6.6, 198.51.100.20"},
			trusted:    loopback,
			want:       "198.51.100.20",
		},
		{
			name:       "a trusted hop in the middle of the chain is skipped",
			remoteAddr: "127.0.0.1:41000",
			forwarded:  []string{"6.6.6.6, 198.51.100.20, 10.0.0.9"},
			trusted:    mustPrefixes(t, "127.0.0.1/32,10.0.0.0/8"),
			want:       "198.51.100.20",
		},
		{
			name:       "the header repeated on several lines is one chain",
			remoteAddr: "127.0.0.1:41000",
			forwarded:  []string{"6.6.6.6", "198.51.100.20"},
			trusted:    loopback,
			want:       "198.51.100.20",
		},
		{
			name:       "a malformed hop is ignored",
			remoteAddr: "127.0.0.1:41000",
			forwarded:  []string{"198.51.100.20, not-an-ip"},
			trusted:    loopback,
			want:       "198.51.100.20",
		},
		{
			name:       "trusted proxy with no header is the client",
			remoteAddr: "127.0.0.1:41000",
			want:       "127.0.0.1",
		},
		{
			name:       "a header of only trusted hops falls back to the proxy",
			remoteAddr: "127.0.0.1:41000",
			forwarded:  []string{"127.0.0.1, ::1"},
			trusted:    loopback,
			want:       "127.0.0.1",
		},
		{
			name:       "Lambda passes the bare source address",
			remoteAddr: "198.51.100.30",
			forwarded:  []string{"6.6.6.6"},
			trusted:    loopback,
			want:       "198.51.100.30",
		},
		{
			name:       "bare loopback is still a trusted proxy",
			remoteAddr: "127.0.0.1",
			forwarded:  []string{"198.51.100.20"},
			trusted:    loopback,
			want:       "198.51.100.20",
		},
		{
			name:       "IPv6 client",
			remoteAddr: "[2001:db8::7]:443",
			forwarded:  []string{"6.6.6.6"},
			trusted:    loopback,
			want:       "2001:db8::7",
		},
		{
			name:       "IPv6 loopback proxy forwarding an IPv6 client",
			remoteAddr: "[::1]:41000",
			forwarded:  []string{"2001:db8::7"},
			trusted:    loopback,
			want:       "2001:db8::7",
		},
		{
			name:       "IPv4-mapped loopback is trusted",
			remoteAddr: "[::ffff:127.0.0.1]:41000",
			forwarded:  []string{"198.51.100.20"},
			trusted:    loopback,
			want:       "198.51.100.20",
		},
		{
			name:       "empty remote address stays empty",
			remoteAddr: "",
			forwarded:  []string{"6.6.6.6"},
			trusted:    loopback,
			want:       "",
		},
		{
			name:       "unparsable remote address stays empty",
			remoteAddr: "garbage",
			forwarded:  []string{"6.6.6.6"},
			trusted:    loopback,
			want:       "",
		},
		{
			name:       "no trusted proxies never reads the header",
			remoteAddr: "127.0.0.1:41000",
			forwarded:  []string{"198.51.100.20"},
			want:       "127.0.0.1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveClientIP(tt.remoteAddr, tt.forwarded, tt.trusted); got != tt.want {
				t.Fatalf("resolveClientIP(%q, %q) = %q, want %q", tt.remoteAddr, tt.forwarded, got, tt.want)
			}
		})
	}
}

func TestParse_TrustedProxies(t *testing.T) {
	t.Run("cidrs and bare addresses", func(t *testing.T) {
		got, err := ParseTrustedProxies(" 127.0.0.1/32 , ::1 ,10.0.0.0/8,, ")
		if err != nil {
			t.Fatalf("ParseTrustedProxies: %v", err)
		}
		want := []string{"127.0.0.1/32", "::1/128", "10.0.0.0/8"}
		if len(got) != len(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		for i := range want {
			if got[i].String() != want[i] {
				t.Fatalf("entry %d = %s, want %s", i, got[i], want[i])
			}
		}
	})
	t.Run("empty list trusts nobody", func(t *testing.T) {
		got, err := ParseTrustedProxies("")
		if err != nil || len(got) != 0 {
			t.Fatalf("got %v, %v", got, err)
		}
	})
	t.Run("a malformed entry is an error and keeps the valid ones", func(t *testing.T) {
		got, err := ParseTrustedProxies("127.0.0.1/32,not-a-cidr")
		if err == nil {
			t.Fatal("want an error for not-a-cidr")
		}
		if len(got) != 1 || got[0].String() != "127.0.0.1/32" {
			t.Fatalf("got %v", got)
		}
	})
}

func mustPrefixes(t *testing.T, csv string) []netip.Prefix {
	t.Helper()
	prefixes, err := ParseTrustedProxies(csv)
	if err != nil {
		t.Fatalf("ParseTrustedProxies(%q): %v", csv, err)
	}
	return prefixes
}
