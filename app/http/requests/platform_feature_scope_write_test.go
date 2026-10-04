package requests

import (
	"errors"
	"strings"
	"testing"
)

func TestParsePlatformFeatureScopeWrites(t *testing.T) {
	t.Parallel()

	got, err := ParsePlatformFeatureScopeWrites([]byte(`{"features":[{"key":" sweep-enabled ","enabled":false},{"key":"withdrawals-enabled","enabled":true}]}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 2 || got[0].Key != "sweep-enabled" || got[0].Enabled || !got[1].Enabled || got[1].Key != "withdrawals-enabled" {
		t.Fatalf("writes = %+v", got)
	}

	cases := []struct {
		name string
		body string
		want error
	}{
		{name: "empty", body: ``, want: ErrPlatformFeatureScopeBodyInvalid},
		{name: "array", body: `[]`, want: ErrPlatformFeatureScopeBodyInvalid},
		{name: "missing", body: `{}`, want: ErrPlatformFeatureScopeFeaturesRequired},
		{name: "null", body: `{"features":null}`, want: ErrPlatformFeatureScopeFeaturesRequired},
		{name: "empty list", body: `{"features":[]}`, want: ErrPlatformFeatureScopeFeaturesRequired},
		{name: "blank key", body: `{"features":[{"key":" ","enabled":true}]}`, want: ErrPlatformFeatureScopeBodyInvalid},
		{name: "missing enabled", body: `{"features":[{"key":"sweep-enabled"}]}`, want: ErrPlatformFeatureScopeBodyInvalid},
		{name: "null enabled", body: `{"features":[{"key":"sweep-enabled","enabled":null}]}`, want: ErrPlatformFeatureScopeBodyInvalid},
		{name: "string enabled", body: `{"features":[{"key":"sweep-enabled","enabled":"false"}]}`, want: ErrPlatformFeatureScopeBodyInvalid},
		{name: "extra field", body: `{"features":[{"key":"sweep-enabled","enabled":false,"note":"x"}]}`, want: ErrPlatformFeatureScopeBodyInvalid},
		{name: "duplicate", body: `{"features":[{"key":"sweep-enabled","enabled":false},{"key":"sweep-enabled","enabled":true}]}`, want: ErrPlatformFeatureScopeDuplicate},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParsePlatformFeatureScopeWrites([]byte(tc.body))
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}

	_, err = ParsePlatformFeatureScopeWrites([]byte(strings.Repeat(" ", maxAccountFeatureBodyBytes+1)))
	if !errors.Is(err, ErrPlatformFeatureScopeBodyTooLarge) {
		t.Fatalf("oversize error = %v", err)
	}
}
