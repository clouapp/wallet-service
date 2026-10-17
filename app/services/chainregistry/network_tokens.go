package chainregistry

import (
	"context"
	"fmt"
	"strings"
)

// TokenChange is one token row the alignment creates, re-activates or deactivates.
type TokenChange struct {
	ChainID  string
	Network  string
	Symbol   string
	Contract string
	Action   string
}

// TokenAlignmentPlan is what the token alignment changed, or would change. Unknown
// lists active tokens no catalog list names ("chain:symbol:contract"); Mainnet the
// primary records on a mainnet network and Missing the ones the registry lacks.
type TokenAlignmentPlan struct {
	Changes []TokenChange
	Unknown []string
	Mainnet []string
	Missing []string
}

// TokenAligner reports, or with apply writes, the token alignment.
type TokenAligner func(ctx context.Context, apply bool) (TokenAlignmentPlan, error)

// NetworkTokens moves the tokens of the primary records that point at a test network
// onto that network's tokens (chains:align-tokens).
type NetworkTokens struct {
	align TokenAligner
}

// NewNetworkTokens wires the aligner.
func NewNetworkTokens(align TokenAligner) *NetworkTokens {
	return &NetworkTokens{align: align}
}

// Align previews the alignment and, when apply is set and there is something to
// change, writes it.
func (n *NetworkTokens) Align(ctx context.Context, apply bool) (Report, error) {
	var report Report
	if n == nil || n.align == nil {
		return report, fmt.Errorf("chains:align-tokens: aligner is required")
	}
	plan, err := n.align(ctx, false)
	if err != nil {
		return report, err
	}
	for _, change := range plan.Changes {
		report.Info = append(report.Info, fmt.Sprintf("%s (%s): %s %s %s", change.ChainID, change.Network, change.Action, change.Symbol, change.Contract))
	}
	for _, token := range plan.Unknown {
		report.Warning = append(report.Warning, fmt.Sprintf("token %s is in no catalog list; left as is", token))
	}
	if len(plan.Mainnet) > 0 {
		report.Info = append(report.Info, "on a mainnet network, left as they are: "+strings.Join(plan.Mainnet, ", "))
	}
	if len(plan.Missing) > 0 {
		report.Info = append(report.Info, "not in the registry: "+strings.Join(plan.Missing, ", "))
	}
	if len(plan.Changes) == 0 {
		report.Info = append(report.Info, "tokens already aligned")
		return report, nil
	}
	if !apply {
		report.Info = append(report.Info, "dry run: nothing written (pass --apply)")
		return report, nil
	}
	applied, err := n.align(ctx, true)
	if err != nil {
		return report, err
	}
	report.Info = append(report.Info, fmt.Sprintf("applied %d token changes; restart the API and workers so the chain adapters reload the tokens", len(applied.Changes)))
	return report, nil
}
