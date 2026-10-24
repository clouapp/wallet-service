package publicurl

import (
	"errors"
	"testing"
)

func TestParseBase_AcceptsAPublicHTTPSOrigin(t *testing.T) {
	cases := map[string]string{
		"https://wallets.example.com":       "https://wallets.example.com",
		"  https://wallets.example.com/  ":  "https://wallets.example.com/",
		"https://wallets.example.com/waas/": "https://wallets.example.com/waas/",
		"https://wallets.example.com:8443":  "https://wallets.example.com:8443",
		"https://8.8.8.8":                   "https://8.8.8.8",
		"https://[2606:4700::1111]":         "https://[2606:4700::1111]",
	}
	for raw, want := range cases {
		parsed, err := ParseBase(raw)
		if err != nil {
			t.Errorf("ParseBase(%q) refused a public https origin: %v", raw, err)
			continue
		}
		if parsed.String() != want {
			t.Errorf("ParseBase(%q) = %q, want %q", raw, parsed.String(), want)
		}
	}
}

func TestParseBase_RefusesWhatAProviderCannotPostTo(t *testing.T) {
	for _, raw := range []string{
		"", "   ", "wallets.example.com", "http://wallets.example.com", "ftp://wallets.example.com", "https://",
		"https://user:pass@wallets.example.com", "https://user@wallets.example.com",
		"https://wallets.example.com/?a=1", "https://wallets.example.com?", "https://wallets.example.com#x",
		"https://wallets.example.com#", "https://wallets.example.com:99999", "https://wallets.example.com:0",
		"https://wallets.example.com:http", "https://wallets.example.com:",
		"https://localhost", "https://LOCALHOST", "https://api.localhost", "https://127.0.0.1", "https://10.0.0.5",
		"https://172.16.4.1", "https://192.168.1.10", "https://169.254.169.254", "https://[::1]", "https://[fd00::1]",
		"https://[fe80::1]", "https://0.0.0.0", "https://100.64.0.1", "https://224.0.0.1", "https://239.255.0.1",
		"https://[ff05::2]", "https://[::ffff:127.0.0.1]", "https://[::ffff:100.64.0.1]", "https://%zz",
	} {
		if _, err := ParseBase(raw); !errors.Is(err, ErrNotPublicBase) {
			t.Errorf("ParseBase(%q) = %v, want ErrNotPublicBase", raw, err)
		}
	}
}
