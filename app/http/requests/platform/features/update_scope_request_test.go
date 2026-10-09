package features

import (
	"errors"
	"strings"
	"testing"
)

func TestUpdateScopeRequest_Parse_ReadsTheBulkBody(t *testing.T) {
	t.Parallel()

	var req UpdateScopeRequest
	err := req.parse([]byte(`{"features":[{"key":" sweep-enabled ","enabled":false},{"key":"withdrawals-enabled","enabled":true}]}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := req.Features
	if len(got) != 2 || got[0].Key != "sweep-enabled" || got[0].Enabled || !got[1].Enabled || got[1].Key != "withdrawals-enabled" {
		t.Fatalf("writes = %+v", got)
	}

	cases := []struct {
		name string
		body string
		want error
	}{
		{name: "empty", body: ``, want: ErrBodyInvalid},
		{name: "array", body: `[]`, want: ErrBodyInvalid},
		{name: "missing", body: `{}`, want: ErrFeaturesRequired},
		{name: "null", body: `{"features":null}`, want: ErrFeaturesRequired},
		{name: "empty list", body: `{"features":[]}`, want: ErrFeaturesRequired},
		{name: "blank key", body: `{"features":[{"key":" ","enabled":true}]}`, want: ErrBodyInvalid},
		{name: "missing enabled", body: `{"features":[{"key":"sweep-enabled"}]}`, want: ErrBodyInvalid},
		{name: "null enabled", body: `{"features":[{"key":"sweep-enabled","enabled":null}]}`, want: ErrBodyInvalid},
		{name: "string enabled", body: `{"features":[{"key":"sweep-enabled","enabled":"false"}]}`, want: ErrBodyInvalid},
		{name: "extra field", body: `{"features":[{"key":"sweep-enabled","enabled":false,"note":"x"}]}`, want: ErrBodyInvalid},
		{name: "duplicate", body: `{"features":[{"key":"sweep-enabled","enabled":false},{"key":"sweep-enabled","enabled":true}]}`, want: ErrDuplicateKey},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := new(UpdateScopeRequest).parse([]byte(tc.body))
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}

	err = new(UpdateScopeRequest).parse([]byte(strings.Repeat(" ", maxBodyBytes+1)))
	if !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("oversize error = %v", err)
	}
}

func TestUpdateScopeRequest_Writes_MapsTheFlagsInOrder(t *testing.T) {
	req := UpdateScopeRequest{Features: []ScopeFlag{{Key: "b", Enabled: true}, {Key: "a", Enabled: false}}}
	got := req.Writes()
	if len(got) != 2 || got[0].Key != "b" || !got[0].Enabled || got[1].Key != "a" || got[1].Enabled {
		t.Fatalf("writes = %+v", got)
	}
	if len((&UpdateScopeRequest{}).Writes()) != 0 {
		t.Fatal("an empty request produced writes")
	}
}
