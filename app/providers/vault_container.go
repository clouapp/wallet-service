package providers

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/goravel/framework/contracts/foundation"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/models"
	mpc "github.com/macrowallets/waas/app/services/mpc"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/app/services/sweep"
)

// openChainEndpoint opens a sealed rpc_url and returns the URL to dial.
// A read failure does not include the URL.
func openChainEndpoint(stored string) (string, error) {
	cipher := facades.Crypt()
	if cipher == nil {
		return "", fmt.Errorf("open chain rpc")
	}
	opened, err := settings.OpenStored(cipher, stored)
	if err != nil {
		return "", fmt.Errorf("open chain rpc")
	}
	endpoint, err := models.DialEndpoint(opened)
	if err != nil {
		return "", fmt.Errorf("open chain rpc")
	}
	return endpoint, nil
}

func registerVaultContainer(app foundation.Application) {
	app.Singleton(container.ContainerKey, func(app foundation.Application) (any, error) {
		c, err := buildVaultContainer(app)
		if err != nil {
			return nil, err
		}
		return c, nil
	})
}

// buildVaultContainer fills the god struct with the instances the providers
// bind, for the callers that still read container.Get.
func buildVaultContainer(app foundation.Application) (*container.Container, error) {
	redisClient, err := facades.Redis()
	if err != nil {
		return nil, fmt.Errorf("vault: redis: %w", err)
	}
	c := &container.Container{Redis: redisClient}
	var mpcService *mpc.TSSService
	var box *sweep.Box
	if err := errors.Join(
		bound(app, &c.SecretsManager),
		bound(app, &mpcService),
		bound(app, &c.UserRepo),
		bound(app, &c.RefreshTokenRepo),
		bound(app, &c.PasswordResetTokenRepo),
		bound(app, &c.TotpRecoveryCodeRepo),
		bound(app, &c.AccountRepo),
		bound(app, &c.AccountUserRepo),
		bound(app, &c.AccessTokenRepo),
		bound(app, &c.WalletRepo),
		bound(app, &c.WalletUserRepo),
		bound(app, &c.AddressRepo),
		bound(app, &c.TransactionRepo),
		bound(app, &c.WithdrawalRepo),
		bound(app, &c.WebhookConfigRepo),
		bound(app, &c.WebhookEventRepo),
		bound(app, &c.WhitelistEntryRepo),
		bound(app, &c.ChainRepo),
		bound(app, &c.TokenRepo),
		bound(app, &c.ChainResourceRepo),
		bound(app, &c.WebhookSubscriptionRepo),
		bound(app, &c.WalletAssetBalanceRepo),
		bound(app, &c.WalletBalanceSnapshotRepo),
		bound(app, &c.WalletUTXORepo),
		bound(app, &c.WalletSyncStateRepo),
		bound(app, &c.CurrencyRepo),
		bound(app, &c.WebhookSyncService),
		bound(app, &c.PriceService),
		bound(app, &c.Registry),
		bound(app, &c.WalletService),
		bound(app, &c.DepositService),
		bound(app, &c.WithdrawalService),
		bound(app, &box),
		bound(app, &c.WebhookService),
		bound(app, &c.WithdrawalEvents),
		bound(app, &c.DepositEvents),
		bound(app, &c.IngestService),
		bound(app, &c.BalanceRefreshService),
		bound(app, &c.WalletRefresher),
	); err != nil {
		return nil, err
	}
	c.MPCService = mpcService
	c.SweepService = box.Service
	return c, nil
}

func bound[T any](app foundation.Application, into *T) error {
	value, err := resolve[T](app)
	if err != nil {
		return err
	}
	*into = value
	return nil
}

// lenientLogScanChains keep their deployed deposit scan: a block whose eth_getLogs
// fails is scanned for native transfers only. Every other EVM record fails the block
// so the scanner retries it (evmchain.EVMConfig.StrictLogScan).
var lenientLogScanChains = map[string]bool{
	models.ChainETH:      true,
	models.ChainTETH:     true,
	models.ChainPolygon:  true,
	models.ChainTPolygon: true,
}

// resolveGasReadinessThreshold is the chains.gas_readiness_threshold_raw value.
// An empty column means the chain has no gas threshold. Environment variables
// do not fill it in.
func resolveGasReadinessThreshold(ch *models.Chain) *big.Int {
	if ch == nil {
		return nil
	}
	return ch.GasReadinessThreshold()
}

// resolveDustThresholdNative is the chains.dust_threshold_native_raw value.
// An empty column means no native dust filter. Environment variables do not
// fill it in.
func resolveDustThresholdNative(ch *models.Chain) *big.Int {
	if ch == nil {
		return nil
	}
	return ch.DustThresholdNative()
}
