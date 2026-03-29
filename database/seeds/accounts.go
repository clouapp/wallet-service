package seeds

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

// SeedPairedAccounts creates prod + test Acme accounts and cross-links them.
func SeedPairedAccounts(_ context.Context) error {
	var prodExisting models.Account
	prodExists := facades.Orm().Query().Where("id", acmeAccountID).First(&prodExisting) == nil && prodExisting.ID != uuid.Nil

	if !prodExists {
		prod := models.Account{
			ID:              acmeAccountID,
			Name:            "Acme Corp",
			Status:          "active",
			ViewAllWallets:  true,
			Environment:     models.EnvironmentProd,
			LinkedAccountID: nil,
		}
		if err := facades.Orm().Query().Create(&prod); err != nil {
			return fmt.Errorf("create prod account: %w", err)
		}
		slog.Info("created prod account", "name", prod.Name)
	} else {
		slog.Info("prod account already exists, skipping create", "id", acmeAccountID)
	}

	var testExisting models.Account
	testExists := facades.Orm().Query().Where("id", acmeTestAccountID).First(&testExisting) == nil && testExisting.ID != uuid.Nil

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
		if err := facades.Orm().Query().Create(&test); err != nil {
			return fmt.Errorf("create test account: %w", err)
		}
		slog.Info("created test account", "name", test.Name)
	} else {
		slog.Info("test account already exists, skipping create", "id", acmeTestAccountID)
	}

	if _, err := facades.Orm().Query().Model(&models.Account{}).Where("id = ?", acmeAccountID).Update(map[string]any{
		"environment":       models.EnvironmentProd,
		"linked_account_id": acmeTestAccountID,
	}); err != nil {
		return fmt.Errorf("link prod account: %w", err)
	}
	if _, err := facades.Orm().Query().Model(&models.Account{}).Where("id = ?", acmeTestAccountID).Update(map[string]any{
		"environment":       models.EnvironmentTest,
		"linked_account_id": acmeAccountID,
	}); err != nil {
		return fmt.Errorf("link test account: %w", err)
	}

	return nil
}
