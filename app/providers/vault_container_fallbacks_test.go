package providers

import (
	"reflect"
	"testing"

	chainpkg "github.com/macrowallets/waas/app/services/chain"
)

func TestBitcoinFallbacks(t *testing.T) {
	defaults := []string{"electrum+ssl://a:1", "https://litecoin-mainnet.gateway.tatum.io"}
	fallbacks := func(urls ...string) []chainpkg.BitcoinFallback {
		out := make([]chainpkg.BitcoinFallback, 0, len(urls))
		for _, u := range urls {
			out = append(out, chainpkg.BitcoinFallback{URL: u, APIKey: "key"})
		}
		return out
	}
	cases := []struct {
		name       string
		configured string
		want       []chainpkg.BitcoinFallback
	}{
		{"unset uses the network defaults", "", fallbacks(defaults...)},
		{"blank entries only use the defaults", " , ,", fallbacks(defaults...)},
		{"configured list replaces the defaults", " https://x.example/api , electrum+ssl://b:2 ", fallbacks("https://x.example/api", "electrum+ssl://b:2")},
		{"none turns fallbacks off", " NONE ", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := bitcoinFallbacks(tc.configured, "key", defaults); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
	if got := bitcoinFallbacks("", "", nil); len(got) != 0 {
		t.Fatalf("no defaults and nothing configured = %+v, want none", got)
	}
}
