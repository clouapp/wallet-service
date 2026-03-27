package webhooksync

import (
	"context"
	"log/slog"
)

func (s *Service) RunReconciliation(ctx context.Context) error {
	subs, err := s.subscriptionRepo.FindAllActive()
	if err != nil {
		return err
	}

	for _, sub := range subs {
		if err := s.SyncChainAddresses(ctx, sub.ChainID); err != nil {
			slog.Error("reconciliation sync failed", "chain", sub.ChainID, "provider", sub.Provider, "error", err)
		}
	}

	slog.Info("reconciliation complete", "subscriptions", len(subs))
	return nil
}
