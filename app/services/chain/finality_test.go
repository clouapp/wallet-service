package chain

import (
	"testing"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/mocks"
)

// policyOnly is an adapter that reports a finality policy and reads nothing.
type policyOnly struct {
	*mocks.MockChain
	policy string
}

func (p policyOnly) FinalityPolicy() string { return p.policy }

// unreportedPolicy is an adapter that does not report a finality policy at all.
type unreportedPolicy struct {
	types.Chain
}

func TestFinality_PolicyOf_IsWhatTheAdapterReportsOrConfirmations(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		adapter types.Chain
		want    string
	}{
		{"finalized tag", policyOnly{mocks.NewMockChain("polygon"), models.FinalityFinalizedTag}, models.FinalityFinalizedTag},
		{"solidified", policyOnly{mocks.NewMockChain("tron"), models.FinalitySolidified}, models.FinalitySolidified},
		{"blank policy", policyOnly{mocks.NewMockChain("eth"), "  "}, models.FinalityConfirmations},
		{"adapter without a policy", unreportedPolicy{mocks.NewMockChain("btc")}, models.FinalityConfirmations},
		{"nil adapter", nil, models.FinalityConfirmations},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FinalityPolicyOf(tc.adapter); got != tc.want {
				t.Fatalf("FinalityPolicyOf = %q, want %q", got, tc.want)
			}
		})
	}
}
