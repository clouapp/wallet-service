// Package seeds provides deterministic seed data for local development and testing.
// Invoked by Goravel artisan: `go run . artisan db:seed` (see database/seeders).
package seeds

import (
	"context"
	"fmt"
	"log/slog"
)

// Run inserts seed data. It is idempotent: existing rows (matched by primary key)
// are skipped so the command is safe to re-run.
func Run(ctx context.Context) error {
	slog.Info("seeding database…")

	if err := SeedChains(ctx); err != nil {
		return fmt.Errorf("seed chains: %w", err)
	}
	if err := SeedTokens(ctx); err != nil {
		return fmt.Errorf("seed tokens: %w", err)
	}
	if err := SeedChainResources(ctx); err != nil {
		return fmt.Errorf("seed chain resources: %w", err)
	}
	if err := SeedPairedAccounts(ctx); err != nil {
		return fmt.Errorf("seed accounts: %w", err)
	}
	if err := SeedUsers(ctx); err != nil {
		return fmt.Errorf("seed users: %w", err)
	}
	if err := SeedAccountUsers(ctx); err != nil {
		return fmt.Errorf("seed account users: %w", err)
	}
	if err := SeedWallets(ctx); err != nil {
		return fmt.Errorf("seed wallets: %w", err)
	}

	slog.Info("seed complete ✓")
	PrintCredentials()
	return nil
}

// Webhook subscriptions are created via provider APIs, not seeded. Expected mapping after setup:
// | chain_id | provider  | network              |
// |----------|-----------|----------------------|
// | eth      | alchemy   | ETH_MAINNET          |
// | polygon  | alchemy   | MATIC_MAINNET        |
// | teth     | alchemy   | ETH_SEPOLIA          |
// | tpolygon | alchemy   | MATIC_AMOY           |
// | sol      | helius    | enhanced             |
// | tsol     | helius    | enhancedDevnet       |
// | btc      | quicknode | bitcoin-mainnet      |
// | tbtc     | quicknode | bitcoin-testnet      |
