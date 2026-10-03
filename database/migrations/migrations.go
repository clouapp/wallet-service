package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
)

// All returns the ordered list of migrations. The schema was consolidated to
// one CREATE-per-table (each migration captures the final state of its table).
// Timestamp prefixes are sequential integers so the file ordering encodes FK
// dependency: chains → accounts → users → account_users → chain_resources →
// tokens → currencies → wallets → addresses → transactions → … etc.
func All() []schema.Migration {
	return []schema.Migration{
		&M00000000000001CreateEnumTypes{},
		&M00000000000010CreateChainsTable{},
		&M00000000000020CreateAccountsTable{},
		&M00000000000030CreateUsersTable{},
		&M00000000000040CreateAccountUsersTable{},
		&M00000000000050CreateChainResourcesTable{},
		&M00000000000060CreateTokensTable{},
		&M00000000000070CreateCurrenciesTable{},
		&M00000000000080CreateWalletsTable{},
		&M00000000000090CreateAddressesTable{},
		&M00000000000100CreateTransactionsTable{},
		&M00000000000110CreateWebhookConfigsTable{},
		&M00000000000120CreateWebhookEventsTable{},
		&M00000000000130CreateWebhookSubscriptionsTable{},
		&M00000000000140CreateAccessTokensTable{},
		&M00000000000150CreateRefreshTokensTable{},
		&M00000000000160CreatePasswordResetTokensTable{},
		&M00000000000170CreateTotpRecoveryCodesTable{},
		&M00000000000180CreateWithdrawalsTable{},
		&M00000000000190CreateWalletUsersTable{},
		&M00000000000200CreateWhitelistEntriesTable{},
		&M00000000000210CreateWalletBalanceSnapshotsTable{},
		&M00000000000220CreateWalletAssetBalancesTable{},
		&M00000000000230CreateWalletUtxosTable{},
		&M00000000000240CreateWalletSyncStatesTable{},
		&M00000000000250AddFailureReasonToWithdrawals{},
		&M00000000000260AddWebhookOwnershipAndDedup{},
		&M00000000000270RenamePolygonNativeMaticToPol{},
		&M00000000000280EnforceNonNegativeAmounts{},
		&M00000000000290CreateSettingsTable{},
		&M00000000000300CreateFeaturesTable{},
		&M00000000000310CreatePlatformAdminsTable{},
		&M00000000000320CreateGlobalFeaturesTable{},
		&M00000000000330CreateAccountActivityTable{},
	}
}
