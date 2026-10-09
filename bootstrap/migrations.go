package bootstrap

import (
	"github.com/goravel/framework/contracts/database/schema"

	"github.com/macrowallets/waas/database/migrations"
)

// Migrations returns the ordered list of migrations. The schema was consolidated
// to one CREATE-per-table (each migration captures the final state of its table).
// Timestamp prefixes are sequential integers so the file ordering encodes FK
// dependency: chains → accounts → users → account_users → chain_resources →
// tokens → currencies → wallets → addresses → transactions → … etc.
//
// `artisan make:migration` appends to the literal below; keep it a literal.
func Migrations() []schema.Migration {
	return []schema.Migration{
		&migrations.M00000000000001CreateEnumTypes{},
		&migrations.M00000000000010CreateChainsTable{},
		&migrations.M00000000000020CreateAccountsTable{},
		&migrations.M00000000000030CreateUsersTable{},
		&migrations.M00000000000040CreateAccountUsersTable{},
		&migrations.M00000000000050CreateChainResourcesTable{},
		&migrations.M00000000000060CreateTokensTable{},
		&migrations.M00000000000070CreateCurrenciesTable{},
		&migrations.M00000000000080CreateWalletsTable{},
		&migrations.M00000000000090CreateAddressesTable{},
		&migrations.M00000000000100CreateTransactionsTable{},
		&migrations.M00000000000110CreateWebhookConfigsTable{},
		&migrations.M00000000000120CreateWebhookEventsTable{},
		&migrations.M00000000000130CreateWebhookSubscriptionsTable{},
		&migrations.M00000000000140CreateAccessTokensTable{},
		&migrations.M00000000000150CreateRefreshTokensTable{},
		&migrations.M00000000000160CreatePasswordResetTokensTable{},
		&migrations.M00000000000170CreateTotpRecoveryCodesTable{},
		&migrations.M00000000000180CreateWithdrawalsTable{},
		&migrations.M00000000000190CreateWalletUsersTable{},
		&migrations.M00000000000200CreateWhitelistEntriesTable{},
		&migrations.M00000000000210CreateWalletBalanceSnapshotsTable{},
		&migrations.M00000000000220CreateWalletAssetBalancesTable{},
		&migrations.M00000000000230CreateWalletUtxosTable{},
		&migrations.M00000000000240CreateWalletSyncStatesTable{},
		&migrations.M00000000000250AddFailureReasonToWithdrawals{},
		&migrations.M00000000000260AddWebhookOwnershipAndDedup{},
		&migrations.M00000000000270RenamePolygonNativeMaticToPol{},
		&migrations.M00000000000280EnforceNonNegativeAmounts{},
		&migrations.M00000000000290CreateSettingsTable{},
		&migrations.M00000000000300CreateFeaturesTable{},
		&migrations.M00000000000310CreatePlatformAdminsTable{},
		&migrations.M00000000000320CreateGlobalFeaturesTable{},
		&migrations.M00000000000330CreateAccountActivityTable{},
		&migrations.M00000000000340AccessTokensSpendingLimitNonNegative{},
		&migrations.M00000000000350AddAccessTokenUsageTimestamps{},
		&migrations.M00000000000420CreateActivityLogTable{},
		&migrations.M00000000000430UniqueDepositPerTransaction{},
		&migrations.M00000000000440SealWebhookConfigSecrets{},
		&migrations.M00000000000450AddTotpLastUsedCounterToUsers{},
		&migrations.M00000000000460AddSessionsRevokedAtToUsers{},
		&migrations.M00000000000470AccountUsersRoleCheck{},
		&migrations.M00000000000480CreateAccountInvitesTable{},
		&migrations.M00000000000490AddUsersSuspendedAt{},
		&migrations.M00000000000500CreateMfaCredentialsTable{},
		&migrations.M00000000000510CreateMfaBackupCodesTable{},
		&migrations.M00000000000520MoveAccountSweepLimitsToSettings{},
		&migrations.M00000000000530DropAccountsSweepLimits{},
		&migrations.M00000000000540SealWebhookConfigSecrets{},
		&migrations.M00000000000550DropLegacyTotpSecret{},
		&migrations.M00000000000560AddAccountInvitesWalletRoles{},
		&migrations.M00000000000570AccessTokensPermissionsJsonb{},
		&migrations.M00000000000580ChainsThresholdsNotNull{},
		&migrations.M00000000000590SeedPlatformSettingsFromEnv{},
	}
}
