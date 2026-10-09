package chains

import (
	"errors"
	"strings"
	"testing"
)

func TestParseObject_Refuses_WhatIsNotOneJSONObject(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		want error
	}{
		"object":       {body: `{"rpc_url":"https://node.example"}`},
		"empty object": {body: `{}`},
		"empty":        {body: ``, want: ErrBodyInvalid},
		"blank":        {body: "  \n", want: ErrBodyInvalid},
		"null":         {body: `null`, want: ErrBodyInvalid},
		"array":        {body: `[]`, want: ErrBodyInvalid},
		"two values":   {body: `{} {}`, want: ErrBodyInvalid},
		"truncated":    {body: `{"a":`, want: ErrBodyInvalid},
		"too large":    {body: `{"a":"` + strings.Repeat("x", maxBodyBytes) + `"}`, want: ErrBodyTooLarge},
	} {
		t.Run(name, func(t *testing.T) {
			fields, err := parseObject([]byte(tc.body))
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if tc.want == nil && fields == nil {
				t.Fatal("a well-formed object came back nil")
			}
		})
	}
}
