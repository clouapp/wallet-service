package chainregistry

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingAligner answers plan and records the apply flag of every call.
type recordingAligner struct {
	plan  TokenAlignmentPlan
	err   error
	calls []bool
}

func (r *recordingAligner) align(_ context.Context, apply bool) (TokenAlignmentPlan, error) {
	r.calls = append(r.calls, apply)
	return r.plan, r.err
}

func sepoliaPlan() TokenAlignmentPlan {
	return TokenAlignmentPlan{
		Changes: []TokenChange{
			{ChainID: "eth", Network: "ethereum-sepolia", Symbol: "USDC", Contract: "0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238", Action: "create"},
			{ChainID: "eth", Network: "ethereum-sepolia", Symbol: "USDC", Contract: "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48", Action: "deactivate"},
		},
		Unknown: []string{"eth:TST:0xa1"},
		Mainnet: []string{"xrp"},
		Missing: []string{"ltc"},
	}
}

func TestNetwork_Tokens_DryRunReportsThePlanAndWritesNothing(t *testing.T) {
	aligner := &recordingAligner{plan: sepoliaPlan()}

	report, err := NewNetworkTokens(aligner.align).Align(context.Background(), false)
	require.NoError(t, err)

	assert.Equal(t, []bool{false}, aligner.calls)
	info := strings.Join(report.Info, "\n")
	assert.Contains(t, info, "eth (ethereum-sepolia): create USDC 0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238")
	assert.Contains(t, info, "eth (ethereum-sepolia): deactivate USDC 0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48")
	assert.Contains(t, info, "on a mainnet network, left as they are: xrp")
	assert.Contains(t, info, "not in the registry: ltc")
	assert.Contains(t, info, "dry run: nothing written (pass --apply)")
	require.Len(t, report.Warning, 1)
	assert.Contains(t, report.Warning[0], "eth:TST:0xa1")
}

func TestNetwork_Tokens_ApplyWritesAfterThePlan(t *testing.T) {
	aligner := &recordingAligner{plan: sepoliaPlan()}

	report, err := NewNetworkTokens(aligner.align).Align(context.Background(), true)
	require.NoError(t, err)

	assert.Equal(t, []bool{false, true}, aligner.calls)
	assert.Contains(t, report.Info[len(report.Info)-1], "applied 2 token changes; restart the API and workers")
}

func TestNetwork_Tokens_AnAlignedRegistryNeedsNoWrite(t *testing.T) {
	aligner := &recordingAligner{plan: TokenAlignmentPlan{Mainnet: []string{"eth"}}}

	report, err := NewNetworkTokens(aligner.align).Align(context.Background(), true)
	require.NoError(t, err)

	assert.Equal(t, []bool{false}, aligner.calls)
	assert.Contains(t, strings.Join(report.Info, "\n"), "tokens already aligned")
}

func TestNetwork_Tokens_AFailedPlanIsReturned(t *testing.T) {
	aligner := &recordingAligner{err: errors.New("db down")}

	_, err := NewNetworkTokens(aligner.align).Align(context.Background(), true)
	require.Error(t, err)
	assert.Equal(t, []bool{false}, aligner.calls)

	_, err = NewNetworkTokens(nil).Align(context.Background(), false)
	require.Error(t, err)
}
