package rules

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A hosted provider's page hands the client an agreement id and an attesting
// one hands it nothing, the user's legal name standing in its place. An entry
// carrying neither gives the service nothing to record.
func TestTosAcceptanceRule(t *testing.T) {
	rule := &TosAcceptanceRule{}
	require.Equal(t, "tos_acceptance", rule.Signature())

	cases := []struct {
		name  string
		entry any
		want  bool
	}{
		{"a hosted provider's agreement", map[string]any{"provider": "bridge", "signed_agreement_id": "sa_1"}, true},
		{"an attesting provider's legal name", map[string]any{"provider": "ozav", "legal_name": "Ana Costa"}, true},
		{"both", map[string]any{"signed_agreement_id": "sa_1", "legal_name": "Ana Costa"}, true},
		// JSON numbers decode as float64 and bind to a non-empty string.
		{"a numeric agreement id", map[string]any{"signed_agreement_id": float64(5)}, true},
		{"neither", map[string]any{"provider": "bridge"}, false},
		{"both empty", map[string]any{"signed_agreement_id": "", "legal_name": ""}, false},
		{"both null", map[string]any{"signed_agreement_id": nil, "legal_name": nil}, false},
		{"not an object", "sa_1", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, rule.Passes(context.Background(), mapData{}, c.entry))
		})
	}
}
