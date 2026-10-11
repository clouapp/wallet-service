package deposit

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

// reachedConfirmation reports whether tx, at confs confirmations, is confirmed by
// the finality policy it was recorded under: confirmations once confs reaches
// RequiredConfs; finalized_tag or solidified once it has at least one confirmation
// and its block is at or below the chain's final block. A final block that cannot
// be read confirms nothing, and the count never stands in for it.
func reachedConfirmation(ctx context.Context, finality *finalityHeights, tx *models.Transaction, confs int) bool {
	policy := tx.FinalityPolicy()
	if policy == models.FinalityConfirmations {
		return confs >= tx.RequiredConfs
	}
	if confs < 1 {
		return false
	}
	return finality.final(ctx, policy, tx.BlockNumber)
}

// finalityHeights reads one chain's final block at most once per tracker pass for
// each policy. A read that fails is kept for the pass too, so the transactions it
// would confirm wait for the next pass instead of asking the node again each.
type finalityHeights struct {
	adapter types.Chain
	read    map[string]finalityHeight
}

type finalityHeight struct {
	block uint64
	ok    bool
}

func newFinalityHeights(adapter types.Chain) *finalityHeights {
	return &finalityHeights{adapter: adapter, read: map[string]finalityHeight{}}
}

// final reports whether block is at or below the chain's final block under policy.
func (h *finalityHeights) final(ctx context.Context, policy string, block int64) bool {
	height, read := h.read[policy]
	if !read {
		height = h.fetch(ctx, policy)
		h.read[policy] = height
	}
	return height.ok && block > 0 && uint64(block) <= height.block
}

func (h *finalityHeights) fetch(ctx context.Context, policy string) finalityHeight {
	block, err := finalBlock(ctx, h.adapter, policy)
	if err != nil {
		slog.Warn("final block unavailable, its transactions stay confirming until the next pass", "chain", h.adapter.ID(), "finality", policy, "error", err)
		return finalityHeight{}
	}
	return finalityHeight{block: block, ok: true}
}

// finalBlock asks the adapter for the newest block that is final under policy: the
// finalized block (EVM) or the solidified block (TRON).
func finalBlock(ctx context.Context, adapter types.Chain, policy string) (uint64, error) {
	switch policy {
	case models.FinalityFinalizedTag:
		reader, ok := adapter.(chain.FinalizedBlockReader)
		if !ok {
			return 0, fmt.Errorf("adapter of %s cannot read the finalized block", adapter.ID())
		}
		return reader.FinalizedBlock(ctx)
	case models.FinalitySolidified:
		reader, ok := adapter.(chain.SolidifiedBlockReader)
		if !ok {
			return 0, fmt.Errorf("adapter of %s cannot read the solidified block", adapter.ID())
		}
		return reader.SolidifiedBlock(ctx)
	}
	return 0, fmt.Errorf("unknown finality policy %q", policy)
}
