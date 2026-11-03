package ramps

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

func TestBankRefusalReadsEveryRouterRefusal(t *testing.T) {
	for name, tc := range map[string]struct {
		err    error
		reason string
		ok     bool
	}{
		"terms unsigned":        {ramp.ErrTosRequired, "tos_required", true},
		"wrapped terms":         {fmt.Errorf("x: %w", ramp.ErrTosRequired), "tos_required", true},
		"kyc pending (blocked)": {&ramp.KycBlocked{Provider: "blindpay", Err: ramp.ErrKycPending}, "kyc_required", true},
		"kyc rejected":          {&ramp.KycBlocked{Provider: "blindpay", Err: ramp.ErrKycRejected}, "kyc_required", true},
		"kyc pending bare":      {ramp.ErrKycPending, "kyc_required", true},
		"kyc rejected bare":     {ramp.ErrKycRejected, "kyc_required", true},
		"integration required":  {ramp.ErrIntegrationRequired, "kyc_required", true},
		"rail not eligible":     {ramp.ErrRailNotEligible, "kyc_required", true},
		"no provider":           {ramp.ErrNoProviderAvailable, "provider_unavailable", true},
		"unknown":               {errors.New("database is down"), "", false},
	} {
		t.Run(name, func(t *testing.T) {
			reason, ok := bankRefusal(tc.err)
			require.Equal(t, tc.ok, ok)
			require.Equal(t, tc.reason, reason)
		})
	}
}
