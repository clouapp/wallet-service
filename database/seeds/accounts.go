package seeds

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
)

// SeedPairedAccounts creates prod + test Acme accounts and cross-links them.
func SeedPairedAccounts(ctx context.Context) error {
	repo := repositories.NewAccountRepository(nil)

	prodExists, err := repo.Exists(ctx, acmeAccountID)
	if err != nil {
		return fmt.Errorf("find prod account: %w", err)
	}
	if !prodExists {
		prod := models.Account{
			ID:              acmeAccountID,
			Name:            "Acme Corp",
			Status:          "active",
			ViewAllWallets:  true,
			Environment:     models.EnvironmentProd,
			LinkedAccountID: nil,
		}
		if err := repo.Create(ctx, &prod); err != nil {
			return fmt.Errorf("create prod account: %w", err)
		}
		slog.Info("created prod account", "name", prod.Name)
	} else {
		slog.Info("prod account already exists, skipping create", "id", acmeAccountID)
	}

	testExists, err := repo.Exists(ctx, acmeTestAccountID)
	if err != nil {
		return fmt.Errorf("find test account: %w", err)
	}
	if !testExists {
		testLinked := acmeAccountID
		test := models.Account{
			ID:              acmeTestAccountID,
			Name:            "Acme Corp (Test)",
			Status:          "active",
			ViewAllWallets:  true,
			Environment:     models.EnvironmentTest,
			LinkedAccountID: &testLinked,
		}
		if err := repo.Create(ctx, &test); err != nil {
			return fmt.Errorf("create test account: %w", err)
		}
		slog.Info("created test account", "name", test.Name)
	} else {
		slog.Info("test account already exists, skipping create", "id", acmeTestAccountID)
	}

	if err := repo.SetEnvironment(ctx, acmeAccountID, models.EnvironmentProd); err != nil {
		return fmt.Errorf("link prod account: %w", err)
	}
	if err := repo.SetLinkedAccountID(ctx, acmeAccountID, acmeTestAccountID); err != nil {
		return fmt.Errorf("link prod account: %w", err)
	}
	if err := repo.SetEnvironment(ctx, acmeTestAccountID, models.EnvironmentTest); err != nil {
		return fmt.Errorf("link test account: %w", err)
	}
	if err := repo.SetLinkedAccountID(ctx, acmeTestAccountID, acmeAccountID); err != nil {
		return fmt.Errorf("link test account: %w", err)
	}

	return nil
}
