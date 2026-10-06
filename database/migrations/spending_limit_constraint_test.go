package migrations_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

func TestSpending_Limit_ConstraintRejectsANegativeDailyUSD(t *testing.T) {
	fixtures.TestDB(t)
	require.Equal(t, int64(1), constraintCount(t, "access_tokens_spending_limit_daily_usd_non_negative"))

	accountID := uuid.New()
	exec(t, `INSERT INTO accounts (id, name, status, environment, created_at, updated_at)
		VALUES (?, 'spend limit', 'active', 'prod', NOW(), NOW())`, accountID)

	_, err := facades.Orm().Query().Exec(
		`INSERT INTO access_tokens (id, account_id, name, token_hash, spending_limit, created_at, updated_at)
		 VALUES (?, ?, 'negative', 'not-a-secret', '{"daily_usd":"-1"}', NOW(), NOW())`,
		uuid.New(), accountID,
	)
	require.Error(t, err)

	exec(t, `INSERT INTO access_tokens (id, account_id, name, token_hash, spending_limit, created_at, updated_at)
		VALUES (?, ?, 'blank', 'not-a-secret', '{}', NOW(), NOW())`, uuid.New(), accountID)
	exec(t, `INSERT INTO access_tokens (id, account_id, name, token_hash, spending_limit, created_at, updated_at)
		VALUES (?, ?, 'capped', 'not-a-secret', '{"daily_usd":"12.50"}', NOW(), NOW())`, uuid.New(), accountID)
}
