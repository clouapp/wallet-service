package egress

import (
	"errors"
	"net/netip"
	"strings"
	"testing"
)

// webhook is the policy of a webhook receiver: the zero value.
var webhook = Policy{}

func TestPolicy_CheckURL_RefusesEveryInternalDestinationOfAWebhook(t *testing.T) {
	cases := []struct {
		name   string
		url    string
		reason string
	}{
		{"plain http", "http://hooks.example.com/in", ReasonScheme},
		{"another scheme", "ftp://hooks.example.com/in", ReasonInvalidURL},
		{"no scheme", "//hooks.example.com/in", ReasonInvalidURL},
		{"opaque", "https:hooks.example.com", ReasonInvalidURL},
		{"no host", "https:///in", ReasonInvalidURL},
		{"empty", "", ReasonInvalidURL},
		{"port out of range", "https://hooks.example.com:99999/in", ReasonInvalidURL},
		{"port zero", "https://hooks.example.com:0/in", ReasonInvalidURL},
		{"a non-ascii host", "https://hôoks.example.com/in", ReasonHostName},
		{"credentials", "https://user:secret@hooks.example.com/in", ReasonUserinfo},
		{"an empty userinfo", "https://@hooks.example.com/in", ReasonUserinfo},

		{"cloud metadata", "http://169.254.169.254/latest/meta-data/", ReasonLinkLocal},
		{"cloud metadata over https", "https://169.254.169.254/latest/meta-data/", ReasonLinkLocal},
		{"ECS credentials", "https://169.254.170.2/v2/credentials", ReasonLinkLocal},
		{"IPv6 link-local", "https://[fe80::1]/in", ReasonLinkLocal},
		{"IPv6 link-local with a zone", "https://[fe80::1%25eth0]/in", ReasonInvalidURL},
		{"AWS IPv6 metadata", "https://[fd00:ec2::254]/latest/meta-data/", ReasonLinkLocal},
		{"Alibaba metadata", "https://100.100.100.200/latest/meta-data/", ReasonLinkLocal},
		{"Azure wire server", "https://168.63.129.16/machine", ReasonLinkLocal},
		{"IPv4-mapped metadata", "https://[::ffff:169.254.169.254]/latest/meta-data/", ReasonLinkLocal},
		{"IPv4-mapped metadata in hex", "https://[::ffff:a9fe:a9fe]/latest/meta-data/", ReasonLinkLocal},
		{"NAT64 of metadata", "https://[64:ff9b::a9fe:a9fe]/latest/meta-data/", ReasonLinkLocal},
		{"6to4 of a private address", "https://[2002:a00:1::]/in", ReasonPrivate},
		{"metadata name", "https://metadata.google.internal/computeMetadata/v1/", ReasonMetadata},
		{"metadata name with a trailing dot", "https://Metadata.Google.Internal./computeMetadata/v1/", ReasonMetadata},
		{"metadata alias", "https://metadata.goog/computeMetadata/v1/", ReasonMetadata},
		{"EC2 instance data", "https://instance-data.ec2.internal/latest/", ReasonMetadata},

		{"loopback", "https://127.0.0.1/in", ReasonLoopback},
		{"loopback with a port", "https://127.0.0.1:8200/v1/sys/health", ReasonLoopback},
		{"loopback over http", "http://127.0.0.1:3000/in", ReasonLoopback},
		{"loopback range", "https://127.8.9.10/in", ReasonLoopback},
		{"IPv6 loopback", "https://[::1]/in", ReasonLoopback},
		{"IPv4-mapped loopback", "https://[::ffff:127.0.0.1]/in", ReasonLoopback},
		{"localhost", "https://localhost/in", ReasonLoopback},
		{"a localhost subdomain", "https://vault.localhost/in", ReasonLoopback},

		{"private 10/8", "https://10.0.0.1/in", ReasonPrivate},
		{"private 172.16/12", "https://172.17.0.1:8200/v1/sys/health", ReasonPrivate},
		{"private 192.168/16", "https://192.168.1.10/in", ReasonPrivate},
		{"CGNAT", "https://100.64.0.1/in", ReasonPrivate},
		{"CGNAT top", "https://100.127.255.254/in", ReasonPrivate},
		{"IPv6 unique local", "https://[fd12:3456::1]/in", ReasonPrivate},
		{"IPv4-mapped private", "https://[::ffff:10.0.0.1]/in", ReasonPrivate},

		{"unspecified", "https://0.0.0.0/in", ReasonReserved},
		{"this network", "https://0.1.2.3/in", ReasonReserved},
		{"IPv6 unspecified", "https://[::]/in", ReasonReserved},
		{"IPv4-compatible IPv6", "https://[::a00:1]/in", ReasonReserved},
		{"multicast", "https://224.0.0.1/in", ReasonReserved},
		{"IPv6 multicast", "https://[ff02::1]/in", ReasonReserved},
		{"broadcast", "https://255.255.255.255/in", ReasonReserved},
		{"reserved 240/4", "https://240.0.0.1/in", ReasonReserved},
		{"documentation", "https://192.0.2.10/in", ReasonReserved},
		{"benchmarking", "https://198.18.0.1/in", ReasonReserved},
		{"IPv6 documentation", "https://[2001:db8::1]/in", ReasonReserved},

		{"single label", "https://openbao/v1/sys/health", ReasonHostName},
		{"a service name", "https://wallet-service:2002/health", ReasonHostName},
		{"another service name", "http://macro-markets-back/api/webhooks", ReasonHostName},
		{"an internal suffix", "https://vault.internal/v1", ReasonHostName},
		{"an mDNS name", "https://printer.local/in", ReasonHostName},
		{"a cluster service", "https://kubernetes.default.svc/api", ReasonHostName},
		{"a home network", "https://router.home.arpa/in", ReasonHostName},
		{"decimal IPv4", "https://2130706433/in", ReasonHostName},
		{"hex IPv4", "https://0x7f000001/in", ReasonHostName},
		{"octal IPv4", "https://0177.0.0.1/in", ReasonHostName},
		{"short IPv4", "https://127.1/in", ReasonHostName},
		{"hex dotted IPv4", "https://0x7f.0.0.1/in", ReasonHostName},
		{"numeric last label", "https://hooks.example.123/in", ReasonHostName},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := webhook.CheckURL(tc.url)
			assertRefusal(t, err, tc.reason)
			if tc.url != "" && strings.Contains(err.Error(), tc.url) {
				t.Fatalf("the refusal %q carries the url", err)
			}
		})
	}
}

func TestPolicy_CheckURL_AllowsAPublicHTTPSHost(t *testing.T) {
	for _, raw := range []string{
		"https://hooks.example.com/in",
		"https://HOOKS.Example.com./in?x=1#frag",
		"https://hooks.example.com:8443/in",
		"https://a_b.example.com/in",
		"https://xn--hoks-sqa.example.com/in",
		"https://93.184.216.34/in",
		"https://8.8.8.8:443/in",
		"https://[2606:4700:4700::1111]/in",
		"https://100.63.255.255/in",
		"https://100.128.0.1/in",
		"https://172.32.0.1/in",
		"https://169.255.0.1/in",
		"  https://hooks.example.com/in  ",
	} {
		if err := webhook.CheckURL(raw); err != nil {
			t.Errorf("%s refused: %v", raw, err)
		}
	}
}

func TestPolicy_CheckURL_AllowsHTTPOnlyWithTheFlag(t *testing.T) {
	if err := (Policy{AllowHTTP: true}).CheckURL("http://hooks.example.com/in"); err != nil {
		t.Fatalf("http with the flag: %v", err)
	}
	assertRefusal(t, (Policy{AllowHTTP: true}).CheckURL("http://10.0.0.1/in"), ReasonPrivate)
	assertRefusal(t, (Policy{AllowHTTP: true}).CheckURL("http://169.254.169.254/latest/"), ReasonLinkLocal)
}

func TestPolicy_CheckURL_AllowsLoopbackOverHTTPOnlyWhenLoopbackIsAllowed(t *testing.T) {
	tests := Policy{AllowLoopback: true}
	for _, raw := range []string{"http://127.0.0.1:3000/in", "https://127.0.0.1/in", "http://[::1]:8080/in"} {
		if err := tests.CheckURL(raw); err != nil {
			t.Errorf("%s refused: %v", raw, err)
		}
	}
	// Loopback lets a test receiver in, nothing else.
	assertRefusal(t, tests.CheckURL("http://hooks.example.com/in"), ReasonScheme)
	assertRefusal(t, tests.CheckURL("http://93.184.216.34/in"), ReasonScheme)
	assertRefusal(t, tests.CheckURL("http://10.0.0.1/in"), ReasonPrivate)
	assertRefusal(t, tests.CheckURL("http://169.254.169.254/latest/"), ReasonLinkLocal)
	assertRefusal(t, tests.CheckURL("http://0.0.0.0:3000/in"), ReasonReserved)
}

func TestRPCPolicy_AllowsPrivateNodesButNotMetadata(t *testing.T) {
	rpc := RPCPolicy(false)
	for _, raw := range []string{
		"http://10.0.0.5:8545",
		"http://172.17.0.1:8545",
		"http://192.168.1.20:8332",
		"http://100.70.1.2:8545",
		"http://[fd12::5]:8545",
		"http://127.0.0.1:8545",
		"http://localhost:8545",
		"http://geth:8545",
		"https://node.us-east-1.compute.internal/rpc",
		"https://user:secret@btc.example.com/",
		"https://eth-sepolia.g.alchemy.com/v2/key",
	} {
		if err := rpc.CheckURL(raw); err != nil {
			t.Errorf("%s refused: %v", raw, err)
		}
	}
	refused := []struct{ url, reason string }{
		{"http://169.254.169.254/latest/meta-data/", ReasonLinkLocal},
		{"http://169.254.170.2/v2/credentials", ReasonLinkLocal},
		{"http://[fe80::1]:8545", ReasonLinkLocal},
		{"http://[fd00:ec2::254]/latest/", ReasonLinkLocal},
		{"http://100.100.100.200/latest/", ReasonLinkLocal},
		{"http://metadata.google.internal/computeMetadata/v1/", ReasonMetadata},
		{"http://0.0.0.0:8545", ReasonReserved},
		{"http://224.0.0.1:8545", ReasonReserved},
		{"http://0x7f000001:8545", ReasonHostName},
		{"ws://node.example.com", ReasonInvalidURL},
	}
	for _, tc := range refused {
		assertRefusal(t, rpc.CheckURL(tc.url), tc.reason)
	}
}

func TestRPCPolicy_AllowsLinkLocalOnlyWithTheFlag(t *testing.T) {
	rpc := RPCPolicy(true)
	for _, raw := range []string{"http://169.254.169.254/", "http://[fe80::1]:8545", "http://metadata.google.internal/"} {
		if err := rpc.CheckURL(raw); err != nil {
			t.Errorf("%s refused with the flag: %v", raw, err)
		}
	}
	assertRefusal(t, rpc.CheckURL("http://0.0.0.0:8545"), ReasonReserved)
}

func TestPolicy_CheckAddr_ClassifiesEveryRange(t *testing.T) {
	cases := []struct {
		addr   string
		reason string
	}{
		{"127.0.0.1", ReasonLoopback},
		{"::1", ReasonLoopback},
		{"10.255.255.255", ReasonPrivate},
		{"172.31.255.255", ReasonPrivate},
		{"192.168.0.1", ReasonPrivate},
		{"100.64.0.0", ReasonPrivate},
		{"fc00::1", ReasonPrivate},
		{"169.254.0.1", ReasonLinkLocal},
		{"fe80::abcd", ReasonLinkLocal},
		{"::ffff:169.254.169.254", ReasonLinkLocal},
		{"0.0.0.0", ReasonReserved},
		{"::", ReasonReserved},
		{"239.255.255.250", ReasonReserved},
		{"ff05::2", ReasonReserved},
		{"255.255.255.255", ReasonReserved},
		{"203.0.113.9", ReasonReserved},
		{"198.51.100.9", ReasonReserved},
		{"192.0.0.8", ReasonReserved},
		{"fec0::1", ReasonReserved},
		{"100::1", ReasonReserved},
		{"64:ff9b::7f00:1", ReasonLoopback},
		{"2002:7f00:1::", ReasonLoopback},
		{"2002:a9fe:a9fe::", ReasonLinkLocal},
		{"::ffff:100.100.100.200", ReasonLinkLocal},
		{"::ffff:168.63.129.16", ReasonLinkLocal},
		{"::ffff:100.64.0.1", ReasonPrivate},
		{"::ffff:192.0.2.1", ReasonReserved},
		{"::ffff:240.0.0.1", ReasonReserved},
	}
	for _, tc := range cases {
		t.Run(tc.addr, func(t *testing.T) {
			assertRefusal(t, webhook.CheckAddr(netip.MustParseAddr(tc.addr)), tc.reason)
		})
	}
	for _, public := range []string{"1.1.1.1", "93.184.216.34", "2606:4700::1111", "64:ff9b::808:808", "2002:808:808::"} {
		if err := webhook.CheckAddr(netip.MustParseAddr(public)); err != nil {
			t.Errorf("%s refused: %v", public, err)
		}
	}
	assertRefusal(t, webhook.CheckAddr(netip.Addr{}), ReasonInvalidURL)
}

func TestRefusal_MatchesErrRefusedAndNamesOnlyTheRule(t *testing.T) {
	err := webhook.CheckURL("https://10.0.0.1/in")
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("%v does not match ErrRefused", err)
	}
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Reason != ReasonPrivate || err.Error() != ReasonPrivate {
		t.Fatalf("refusal = %#v, text %q", refusal, err)
	}
	if errors.Is(errors.New(ReasonPrivate), ErrRefused) {
		t.Fatal("another error with the same text matched ErrRefused")
	}
}

func TestHost_NamesOnlyTheHost(t *testing.T) {
	cases := map[string]string{
		"https://user:secret@Hooks.Example.com:8443/in?token=abc": "hooks.example.com",
		"http://[::ffff:169.254.169.254]/latest":                  "::ffff:169.254.169.254",
		"http://169.254.169.254/latest/meta-data/iam":             "169.254.169.254",
		"not a url at all": "",
		"https://%zz/":     "",
	}
	for raw, want := range cases {
		if got := Host(raw); got != want {
			t.Errorf("Host(%q) = %q, want %q", raw, got, want)
		}
	}
}

func assertRefusal(t *testing.T, err error, reason string) {
	t.Helper()
	var refusal *Refusal
	if !errors.As(err, &refusal) {
		t.Fatalf("err = %v, want a refusal %q", err, reason)
	}
	if refusal.Reason != reason {
		t.Fatalf("reason = %q, want %q", refusal.Reason, reason)
	}
}
