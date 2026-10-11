package chain

import (
	"context"
	"strings"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
)

// FinalityPolicyReporter is an adapter built with its chain row's finality policy
// (chains.finality): models.FinalityConfirmations, FinalityFinalizedTag or
// FinalitySolidified.
type FinalityPolicyReporter interface {
	FinalityPolicy() string
}

// FinalizedBlockReader is an EVM adapter that reads the newest block its node
// reports as finalized (eth_getBlockByNumber "finalized"); a block at or below it
// is never reorganized. It fails when the node reports no finalized block.
type FinalizedBlockReader interface {
	FinalizedBlock(ctx context.Context) (uint64, error)
}

// SolidifiedBlockReader is a TRON adapter that reads the newest solidified block
// (/walletsolidity), which more than two thirds of the super representatives have
// confirmed and which is never reorganized.
type SolidifiedBlockReader interface {
	SolidifiedBlock(ctx context.Context) (uint64, error)
}

// FinalityPolicyOf is the finality policy a new transaction of adapter's chain is
// recorded with: the policy the adapter reports, or confirmations when it reports
// none.
func FinalityPolicyOf(adapter types.Chain) string {
	reporter, ok := adapter.(FinalityPolicyReporter)
	if !ok {
		return models.FinalityConfirmations
	}
	policy := strings.TrimSpace(reporter.FinalityPolicy())
	if policy == "" {
		return models.FinalityConfirmations
	}
	return policy
}
