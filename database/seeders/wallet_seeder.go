package seeders

import (
	"context"

	"github.com/macrowallets/waas/database/seeds"
)

// WalletSeeder seeds demo wallets and wallet_users.
type WalletSeeder struct{}

func (s *WalletSeeder) Signature() string {
	return "WalletSeeder"
}

func (s *WalletSeeder) Run() error {
	return seeds.SeedWallets(context.Background())
}
