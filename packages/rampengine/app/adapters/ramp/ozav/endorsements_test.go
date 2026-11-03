package ozav

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

func TestSynthesizeEndorsement(t *testing.T) {
	cases := []struct {
		name            string
		status, verMode string
		wantStatus      string
	}{
		{"approved with a verification mode clears pix", "approved", "full", models.EndorsementStatusApproved},
		{"approved without a verification mode does not clear pix", "approved", "", models.EndorsementStatusIncomplete},
		{"pending never clears pix", "pending", "", models.EndorsementStatusIncomplete},
		{"in_review never clears pix", "in_review", "", models.EndorsementStatusIncomplete},
		{"rejected never clears pix, even with a mode on record", "rejected", "full", models.EndorsementStatusIncomplete},
		{"suspended never clears pix, even with a mode on record", "suspended", "full", models.EndorsementStatusIncomplete},
		{"unknown status never clears pix", "some_new_state", "full", models.EndorsementStatusIncomplete},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := synthesizeEndorsement(c.status, c.verMode)
			require.Equal(t, models.EndorsementPix, e.Name)
			require.Equal(t, c.wantStatus, e.Status)
		})
	}
}

// The router grants a rail on the umbrella "pix" name or the
// direction-specific one (app/models/user_provider_kyc.go railEndorsements).
// Ozav's approval is not per-direction, so the synthesized name must be the
// umbrella one — this pins that choice so a future edit cannot narrow it to
// one direction by accident.
func TestSynthesizeEndorsementUsesUmbrellaNameForBothRails(t *testing.T) {
	e := synthesizeEndorsement("approved", "full")
	require.Equal(t, "pix", e.Name)
}
