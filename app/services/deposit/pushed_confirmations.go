package deposit

import (
	"context"
	"log/slog"
)

// ConfirmChains runs the confirmation step of chainIDs (deposits, withdrawals, sweeps
// and gas seeds) at each chain's head, under its scan lock: for the chains whose
// deposits the provider push records and that no scan of this process reads, whose
// confirmations nothing else moves (a scan confirms the chains it reads at the end
// of each cycle). A chain another scan holds is left to it; one the registry does
// not hold is logged and skipped.
func (s *Service) ConfirmChains(ctx context.Context, chainIDs []string) error {
	for _, chainID := range chainIDs {
		if err := ctx.Err(); err != nil {
			return err
		}
		adapter, err := s.registry.Chain(chainID)
		if err != nil {
			slog.Warn("confirmations of a pushed chain skipped: the chain is not registered", "chain", chainID, "error", err)
			continue
		}
		s.confirmWhileSigning(ctx, chainID, adapter)
	}
	return nil
}
