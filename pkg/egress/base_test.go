package egress

import (
	"errors"
	"strings"
	"testing"
)

func TestParsePublicBase_AcceptsAPublicHTTPSOrigin(t *testing.T) {
	cases := map[string]string{
		"https://wallets.example.com":        "https://wallets.example.com",
		"  https://wallets.example.com/  ":   "https://wallets.example.com/",
		"https://wallets.example.com/waas/":  "https://wallets.example.com/waas/",
		"https://wallets.example.com:8443":   "https://wallets.example.com:8443",
		"https://wallets.example.com.":       "https://wallets.example.com.",
		"https://8.8.8.8":                    "https://8.8.8.8",
		"https://[2606:4700::1111]/ingest":   "https://[2606:4700::1111]/ingest",
		"HTTPS://Wallets.Example.com/Ingest": "https://Wallets.Example.com/Ingest",
	}
	for raw, want := range cases {
		parsed, err := ParsePublicBase(raw)
		if err != nil {
			t.Errorf("ParsePublicBase(%q) refused a public https origin: %v", raw, err)
			continue
		}
		if parsed.String() != want {
			t.Errorf("ParsePublicBase(%q) = %q, want %q", raw, parsed.String(), want)
		}
	}
}

func TestParsePublicBase_RefusesWhatAProviderCannotPostTo(t *testing.T) {
	cases := []struct {
		raw    string
		reason string
	}{
		{"", ReasonInvalidURL},
		{"   ", ReasonInvalidURL},
		{"wallets.example.com", ReasonInvalidURL},
		{"https://", ReasonInvalidURL},
		{"https://%zz", ReasonInvalidURL},
		{"ftp://wallets.example.com", ReasonInvalidURL},
		{"http://wallets.example.com", ReasonScheme},
		{"https://user:pass@wallets.example.com", ReasonUserinfo},
		{"https://user@wallets.example.com", ReasonUserinfo},
		{"https://wallets.example.com/?a=1", ReasonBaseURL},
		{"https://wallets.example.com?", ReasonBaseURL},
		{"https://wallets.example.com#x", ReasonBaseURL},
		{"https://wallets.example.com#", ReasonBaseURL},
		{"https://wallets.example.com:", ReasonBaseURL},
		{"https://wallets.example.com:99999", ReasonInvalidURL},
		{"https://wallets.example.com:0", ReasonInvalidURL},
		{"https://wallets.example.com:http", ReasonInvalidURL},
		{"https://wallets.example.com:22", ReasonPort},
		{"https://wallets.example.com:25", ReasonPort},

		// loopback, in every spelling
		{"https://localhost", ReasonLoopback},
		{"https://LOCALHOST", ReasonLoopback},
		{"https://localhost.", ReasonLoopback},
		{"https://api.localhost", ReasonLoopback},
		{"https://127.0.0.1", ReasonLoopback},
		{"https://127.0.0.1.", ReasonLoopback},
		{"https://[::1]", ReasonLoopback},
		{"https://[::ffff:127.0.0.1]", ReasonLoopback},
		{"https://[::127.0.0.1]", ReasonReserved},
		{"https://2130706433", ReasonHostName},
		{"https://0x7f000001", ReasonHostName},
		{"https://0177.0.0.1", ReasonHostName},
		{"https://127.1", ReasonHostName},

		// cloud metadata and link-local
		{"https://169.254.169.254", ReasonLinkLocal},
		{"https://169.254.169.254.", ReasonLinkLocal},
		{"https://[fe80::1]", ReasonLinkLocal},
		{"https://[64:ff9b::a9fe:a9fe]", ReasonLinkLocal},
		{"https://[fd00:ec2::254]", ReasonLinkLocal},
		{"https://metadata.google.internal", ReasonMetadata},

		// private, shared and internal names
		{"https://10.0.0.5", ReasonPrivate},
		{"https://172.16.4.1", ReasonPrivate},
		{"https://192.168.1.10", ReasonPrivate},
		{"https://[fd00::1]", ReasonPrivate},
		{"https://100.64.0.1", ReasonPrivate},
		{"https://[::ffff:100.64.0.1]", ReasonPrivate},
		{"https://foo.internal", ReasonHostName},
		{"https://wallets", ReasonHostName},

		// unspecified, multicast, broadcast and reserved
		{"https://0.0.0.0", ReasonReserved},
		{"https://0.1.2.3", ReasonReserved},
		{"https://255.255.255.255", ReasonReserved},
		{"https://198.18.0.1", ReasonReserved},
		{"https://240.0.0.1", ReasonReserved},
		{"https://224.0.0.1", ReasonReserved},
		{"https://239.255.0.1", ReasonReserved},
		{"https://[ff05::2]", ReasonReserved},
	}
	for _, tc := range cases {
		parsed, err := ParsePublicBase(tc.raw)
		if !errors.Is(err, ErrRefused) || parsed != nil {
			t.Errorf("ParsePublicBase(%q) = %v, %v; want a refusal", tc.raw, parsed, err)
			continue
		}
		if err.Error() != tc.reason {
			t.Errorf("ParsePublicBase(%q) reason = %q, want %q", tc.raw, err.Error(), tc.reason)
		}
		if host := Host(tc.raw); host != "" && strings.Contains(err.Error(), host) {
			t.Errorf("ParsePublicBase(%q) named the host: %q", tc.raw, err.Error())
		}
	}
}
