package seeds

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

// SeedAccountUsers links users to prod and test accounts with roles.
func SeedAccountUsers(_ context.Context) error {
	members := []struct {
		id        uuid.UUID
		accountID uuid.UUID
		userID    uuid.UUID
		role      string
	}{
		{uuid.MustParse("00000000-0000-0000-0000-000000000030"), acmeAccountID, adminUserID, "owner"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000031"), acmeAccountID, aliceUserID, "admin"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000032"), acmeAccountID, bobUserID, "auditor"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000033"), acmeTestAccountID, adminUserID, "owner"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000034"), acmeTestAccountID, aliceUserID, "admin"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000035"), acmeTestAccountID, bobUserID, "auditor"},
	}
	for _, m := range members {
		var existing models.AccountUser
		if err := facades.Orm().Query().Where("id", m.id).First(&existing); err == nil && existing.ID != uuid.Nil {
			continue
		}
		au := models.AccountUser{
			ID:        m.id,
			AccountID: m.accountID,
			UserID:    m.userID,
			Role:      m.role,
			Status:    "active",
		}
		if err := facades.Orm().Query().Create(&au); err != nil {
			return fmt.Errorf("create account_user %s: %w", m.role, err)
		}
		slog.Info("added user to account", "account_id", m.accountID, "role", m.role)
	}
	return nil
}
