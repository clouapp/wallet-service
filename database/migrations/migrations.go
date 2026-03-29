package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
)

func All() []schema.Migration {
	return []schema.Migration{
		&M20260327000001CreateChainsTable{},
		&M20260327000002CreateAccountsTable{},
		&M20260327000003CreateUsersTable{},
		&M20260327000004CreateWalletsTable{},
		&M20260327000005CreateAddressesTable{},
		&M20260327000006CreateTransactionsTable{},
		&M20260327000007CreateWebhookConfigsTable{},
		&M20260327000008CreateWebhookEventsTable{},
		&M20260327000009CreateAccountUsersTable{},
		&M20260327000010CreateAccessTokensTable{},
		&M20260327000011CreateRefreshTokensTable{},
		&M20260327000012CreatePasswordResetTokensTable{},
		&M20260327000013CreateTotpRecoveryCodesTable{},
		&M20260327000014CreateWithdrawalsTable{},
		&M20260327000015CreateWalletUsersTable{},
		&M20260327000016CreateWhitelistEntriesTable{},
		&M20260327000017CreateTokensTable{},
		&M20260327000018CreateChainResourcesTable{},
		&M20260327100001CreateWebhookSubscriptionsTable{},
		&M20260329000001WalletDepositAddressFk{},
	}
}
