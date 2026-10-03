package requests

import (
	"errors"
	"strings"
	"testing"
)

func TestParseAccountFeatureEnabled(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		body    string
		enabled bool
		want    error
	}{
		{name: "on", body: `{"enabled":true}`, enabled: true},
		{name: "off", body: `{"enabled":false}`, enabled: false},
		{name: "missing", body: `{}`, want: ErrAccountFeatureEnabledRequired},
		{name: "null", body: `{"enabled":null}`, want: ErrAccountFeatureEnabledRequired},
		{name: "string", body: `{"enabled":"true"}`, want: ErrAccountFeatureBodyInvalid},
		{name: "number", body: `{"enabled":1}`, want: ErrAccountFeatureBodyInvalid},
		{name: "extra field", body: `{"enabled":true,"key":"x"}`, want: ErrAccountFeatureBodyInvalid},
		{name: "empty", body: ``, want: ErrAccountFeatureBodyInvalid},
		{name: "array", body: `[true]`, want: ErrAccountFeatureBodyInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseAccountFeatureEnabled([]byte(tc.body))
			if tc.want != nil {
				if !errors.Is(err, tc.want) {
					t.Fatalf("error = %v, want %v", err, tc.want)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got != tc.enabled {
				t.Fatalf("enabled = %v, want %v", got, tc.enabled)
			}
		})
	}

	_, err := ParseAccountFeatureEnabled([]byte(strings.Repeat(" ", maxAccountFeatureBodyBytes+1)))
	if !errors.Is(err, ErrAccountFeatureBodyTooLarge) {
		t.Fatalf("oversize error = %v", err)
	}
}
