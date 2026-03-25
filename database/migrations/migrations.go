package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
)

// All returns all database migrations
func All() []schema.Migration {
	return []schema.Migration{
		&M20260317000001CreateWalletsTable{},
		&M20260317000002CreateAddressesTable{},
		&M20260317000003CreateTransactionsTable{},
		&M20260317000004CreateWebhookConfigsTable{},
		&M20260317000005CreateWebhookEventsTable{},
		// Admin panel migrations
		&M20260324000001CreateUsersTable{},
		&M20260324000002CreateAccountsTable{},
		&M20260324000003CreateAccountUsersTable{},
		&M20260324000004CreateAccessTokensTable{},
		&M20260324000005CreateRefreshTokensTable{},
		&M20260324000006CreatePasswordResetTokensTable{},
		&M20260324000007CreateTotpRecoveryCodesTable{},
		&M20260324000008AlterWalletsAddAccountFields{},
		&M20260324000009AlterAddressesAddMetadata{},
		&M20260324000010AlterWebhooksAddWalletId{},
		&M20260324000011AlterTransactionsAddType{},
		&M20260324000012CreateWithdrawalsTable{},
		&M20260324000013CreateWalletUsersTable{},
		&M20260324000014CreateWhitelistEntriesTable{},
		&M20260324000015AlterWalletsAddKeycardFields{},
		&M20260325000001AddActivationCodeRaw{},
	}
}
