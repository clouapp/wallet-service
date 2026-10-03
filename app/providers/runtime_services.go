package providers

import (
	"fmt"

	"github.com/goravel/framework/contracts/foundation"

	"github.com/macrowallets/waas/app/container"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/deposit"
	"github.com/macrowallets/waas/app/services/ingest"
	"github.com/macrowallets/waas/app/services/price"
	"github.com/macrowallets/waas/app/services/refresh"
	"github.com/macrowallets/waas/app/services/sweep"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/app/services/webhooksync"
	"github.com/macrowallets/waas/app/services/withdraw"
	"github.com/macrowallets/waas/app/services/withdrawalevents"
)

// registerRuntimeServices binds the services that buildVaultContainer still
// owns, so routes, jobs and commands can MustMake them. WalletService is not
// bound: recovery tests replace that field after boot, and currentWalletService
// has to observe the replacement.
func registerRuntimeServices(app foundation.Application) {
	bindRuntime(app, func(c *container.Container) *deposit.Service { return c.DepositService }, "deposit service")
	bindRuntime(app, func(c *container.Container) *price.Service { return c.PriceService }, "price service")
	bindRuntime(app, func(c *container.Container) *chainpkg.Registry { return c.Registry }, "chain registry")
	bindRuntime(app, func(c *container.Container) *webhook.Service { return c.WebhookService }, "webhook service")
	bindRuntime(app, func(c *container.Container) *ingest.Service { return c.IngestService }, "ingest service")
	bindRuntime(app, func(c *container.Container) *refresh.BalanceService { return c.BalanceRefreshService }, "balance refresh service")
	bindRuntime(app, func(c *container.Container) *refresh.WalletRefresher { return c.WalletRefresher }, "wallet refresher")
	bindRuntime(app, func(c *container.Container) *withdraw.Service { return c.WithdrawalService }, "withdrawal service")
	bindRuntime(app, func(c *container.Container) *withdrawalevents.Publisher { return c.WithdrawalEvents }, "withdrawal events")
	bindRuntime(app, func(c *container.Container) *webhooksync.Service { return c.WebhookSyncService }, "webhook sync service")
	bindRuntime(app, func(c *container.Container) *authsvc.SecondFactorVerifier {
		verifier, _ := c.SecondFactor.(*authsvc.SecondFactorVerifier)
		return verifier
	}, "second factor verifier")
	bindRuntime(app, func(c *container.Container) *authsvc.TwoFactorLogin {
		login, _ := c.TwoFactorLogin.(*authsvc.TwoFactorLogin)
		return login
	}, "two factor login")
	bindRuntime(app, func(c *container.Container) *authsvc.SessionRevoker {
		revoker, _ := c.SessionRevoker.(*authsvc.SessionRevoker)
		return revoker
	}, "session revoker")

	app.Singleton((*price.CoinAPICredential)(nil), func(foundation.Application) (any, error) {
		return &price.CoinAPICredential{Key: container.Get().PriceConfig.CoinAPIKey}, nil
	})
	app.Singleton((*container.SharedRedis)(nil), func(foundation.Application) (any, error) {
		return &container.SharedRedis{Client: container.Get().Redis}, nil
	})
	app.Singleton((*sweep.Box)(nil), func(foundation.Application) (any, error) {
		service := container.Get().SweepService
		if service == nil {
			return nil, fmt.Errorf("vault: sweep service is not initialized")
		}
		return &sweep.Box{Service: service}, nil
	})
}

func bindRuntime[T any](app foundation.Application, load func(*container.Container) T, name string) {
	var key T
	app.Singleton(key, func(foundation.Application) (any, error) {
		value := load(container.Get())
		if any(value) == nil {
			return nil, fmt.Errorf("vault: %s is not initialized", name)
		}
		return value, nil
	})
}
