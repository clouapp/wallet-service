package features

import (
	"errors"
	"strings"
	"testing"
)

func TestUpdateRequest_Parse_ReadsOnlyTheEnabledBoolean(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		body    string
		enabled bool
		want    error
	}{
		{name: "on", body: `{"enabled":true}`, enabled: true},
		{name: "off", body: `{"enabled":false}`, enabled: false},
		{name: "missing", body: `{}`, want: ErrEnabledRequired},
		{name: "null", body: `{"enabled":null}`, want: ErrEnabledRequired},
		{name: "string", body: `{"enabled":"true"}`, want: ErrBodyInvalid},
		{name: "number", body: `{"enabled":1}`, want: ErrBodyInvalid},
		{name: "extra field", body: `{"enabled":true,"key":"x"}`, want: ErrBodyInvalid},
		{name: "empty", body: ``, want: ErrBodyInvalid},
		{name: "array", body: `[true]`, want: ErrBodyInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var req UpdateRequest
			err := req.parse([]byte(tc.body))
			got := req.Enabled
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

	err := new(UpdateRequest).parse([]byte(strings.Repeat(" ", maxBodyBytes+1)))
	if !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("oversize error = %v", err)
	}
}
