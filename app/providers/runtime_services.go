package providers

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/goravel/framework/contracts/foundation"

	"github.com/macrowallets/waas/app/adapters/redis/feecache"
	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/deposit"
	"github.com/macrowallets/waas/app/services/feeestimate"
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
	app.Singleton((*secretsmanager.Client)(nil), func(foundation.Application) (any, error) {
		client := container.Get().SecretsManager
		if client == nil {
			return nil, fmt.Errorf("secrets manager is not configured")
		}
		return client, nil
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
	app.Singleton((*feeestimate.Service)(nil), func(foundation.Application) (any, error) {
		c := container.Get()
		quoter, ok := c.SweepService.(sweep.FeeQuoter)
		if !ok || quoter == nil {
			return nil, fmt.Errorf("vault: sweep service cannot quote withdrawal fees")
		}
		seconds := facades.Config().GetInt("fee_estimate.cache_ttl_seconds")
		if seconds < 0 {
			seconds = int(feeestimate.DefaultCacheTTL / time.Second)
		}
		return feeestimate.NewService(
			quoter,
			c.Registry,
			feeEstimateChains{repo: c.ChainRepo},
			feecache.New(c.Redis),
			time.Duration(seconds)*time.Second,
			time.Now,
		)
	})
}

// feeEstimateChains adapts the context-aware chain repository to the estimator catalog.
type feeEstimateChains struct {
	repo *repositories.ChainRepository
}

func (c feeEstimateChains) FindByID(id string) (*models.Chain, error) {
	if c.repo == nil {
		return nil, fmt.Errorf("fee estimate: chain repository is required")
	}
	if id == "" {
		return nil, fmt.Errorf("fee estimate: chain id is required")
	}
	return c.repo.FindByID(context.Background(), id)
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
