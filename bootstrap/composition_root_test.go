package bootstrap_test

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sweepsecrets "github.com/macrowallets/waas/app/adapters/secretsmanager"
	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/repositories"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/chainregistry"
	"github.com/macrowallets/waas/app/services/deposit"
	"github.com/macrowallets/waas/app/services/ingest"
	mpc "github.com/macrowallets/waas/app/services/mpc"
	"github.com/macrowallets/waas/app/services/price"
	"github.com/macrowallets/waas/app/services/refresh"
	"github.com/macrowallets/waas/app/services/sweep"
	"github.com/macrowallets/waas/app/services/wallet"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/app/services/webhooksync"
	"github.com/macrowallets/waas/app/services/withdraw"
	"github.com/macrowallets/waas/app/services/withdrawalevents"
)

// The providers key every service by its type (a typed nil pointer); the
// framework keys its own bindings by string. Every typed binding must build,
// hold the type it is keyed by, and come back as the same instance: a
// singleton built twice would split the state of the services that share it.
func TestComposition_Root_ResolvesEveryTypedBindingToOneInstance(t *testing.T) {
	var keys []any
	for _, key := range app.Bindings() {
		if typed := reflect.TypeOf(key); typed != nil && typed.Kind() == reflect.Pointer && reflect.ValueOf(key).IsNil() {
			keys = append(keys, key)
		}
	}
	require.NotEmpty(t, keys, "no typed binding is registered")

	for _, key := range keys {
		t.Run(fmt.Sprintf("%T", key), func(t *testing.T) {
			first, err := app.Make(key)
			require.NoError(t, err)
			require.False(t, container.IsNil(first), "the binding returned nil")
			assert.Equal(t, reflect.TypeOf(key), reflect.TypeOf(first), "the binding returned another type")

			second, err := app.Make(key)
			require.NoError(t, err)
			assert.True(t, first == second, "a second resolution built another instance")
		})
	}
}

// The registry the registry service fills is the one every consumer gets.
func TestComposition_Root_SharesTheChainRegistry(t *testing.T) {
	registry := container.MustMake[*chainpkg.Registry]()
	assert.Same(t, container.MustMake[*chainregistry.ChainRegistryService]().Registry(), registry)
}

// While the vault container still builds services, the instance it holds and
// the one bound by type must be the same object.
func TestComposition_Root_TheVaultContainerHoldsTheBoundInstances(t *testing.T) {
	vault := container.Get()
	held := []struct {
		name  string
		vault any
		bound any
	}{
		{"UserRepo", vault.UserRepo, container.MustMake[*repositories.UserRepository]()},
		{"RefreshTokenRepo", vault.RefreshTokenRepo, container.MustMake[*repositories.RefreshTokenRepository]()},
		{"PasswordResetTokenRepo", vault.PasswordResetTokenRepo, container.MustMake[*repositories.PasswordResetTokenRepository]()},
		{"TotpRecoveryCodeRepo", vault.TotpRecoveryCodeRepo, container.MustMake[*repositories.TotpRecoveryCodeRepository]()},
		{"AccountRepo", vault.AccountRepo, container.MustMake[*repositories.AccountRepository]()},
		{"AccountUserRepo", vault.AccountUserRepo, container.MustMake[*repositories.AccountUserRepository]()},
		{"AccessTokenRepo", vault.AccessTokenRepo, container.MustMake[*repositories.AccessTokenRepository]()},
		{"WalletRepo", vault.WalletRepo, container.MustMake[*repositories.WalletRepository]()},
		{"WalletUserRepo", vault.WalletUserRepo, container.MustMake[*repositories.WalletUserRepository]()},
		{"AddressRepo", vault.AddressRepo, container.MustMake[*repositories.AddressRepository]()},
		{"TransactionRepo", vault.TransactionRepo, container.MustMake[*repositories.TransactionRepository]()},
		{"WithdrawalRepo", vault.WithdrawalRepo, container.MustMake[*repositories.WithdrawalRepository]()},
		{"WebhookConfigRepo", vault.WebhookConfigRepo, container.MustMake[*repositories.WebhookConfigRepository]()},
		{"WebhookEventRepo", vault.WebhookEventRepo, container.MustMake[*repositories.WebhookEventRepository]()},
		{"WhitelistEntryRepo", vault.WhitelistEntryRepo, container.MustMake[*repositories.WhitelistEntryRepository]()},
		{"ChainRepo", vault.ChainRepo, container.MustMake[*repositories.ChainRepository]()},
		{"TokenRepo", vault.TokenRepo, container.MustMake[*repositories.TokenRepository]()},
		{"ChainResourceRepo", vault.ChainResourceRepo, container.MustMake[*repositories.ChainResourceRepository]()},
		{"WebhookSubscriptionRepo", vault.WebhookSubscriptionRepo, container.MustMake[*repositories.WebhookSubscriptionRepository]()},
		{"WalletAssetBalanceRepo", vault.WalletAssetBalanceRepo, container.MustMake[*repositories.WalletAssetBalanceRepository]()},
		{"WalletBalanceSnapshotRepo", vault.WalletBalanceSnapshotRepo, container.MustMake[*repositories.WalletBalanceSnapshotRepository]()},
		{"WalletUTXORepo", vault.WalletUTXORepo, container.MustMake[*repositories.WalletUTXORepository]()},
		{"WalletSyncStateRepo", vault.WalletSyncStateRepo, container.MustMake[*repositories.WalletSyncStateRepository]()},
		{"CurrencyRepo", vault.CurrencyRepo, container.MustMake[*repositories.CurrencyRepository]()},
		{"MPCService", vault.MPCService, container.MustMake[*mpc.TSSService]()},
		{"SecretsManager", vault.SecretsManager, container.MustMake[*sweepsecrets.SDKClient]()},
		{"Registry", vault.Registry, container.MustMake[*chainpkg.Registry]()},
		{"WalletService", vault.WalletService, container.MustMake[*wallet.Service]()},
		{"WebhookService", vault.WebhookService, container.MustMake[*webhook.Service]()},
		{"WebhookSyncService", vault.WebhookSyncService, container.MustMake[*webhooksync.Service]()},
		{"PriceService", vault.PriceService, container.MustMake[*price.Service]()},
		{"SweepService", vault.SweepService, container.MustMake[*sweep.Box]().Service},
		{"WithdrawalService", vault.WithdrawalService, container.MustMake[*withdraw.Service]()},
		{"WithdrawalEvents", vault.WithdrawalEvents, container.MustMake[*withdrawalevents.Publisher]()},
		{"DepositService", vault.DepositService, container.MustMake[*deposit.Service]()},
		{"IngestService", vault.IngestService, container.MustMake[*ingest.Service]()},
		{"BalanceRefreshService", vault.BalanceRefreshService, container.MustMake[*refresh.BalanceService]()},
		{"WalletRefresher", vault.WalletRefresher, container.MustMake[*refresh.WalletRefresher]()},
	}
	for _, field := range held {
		require.False(t, container.IsNil(field.vault), "%s is nil on the vault container", field.name)
		assert.True(t, field.vault == field.bound, "%s on the vault container is not the bound instance", field.name)
	}
}
