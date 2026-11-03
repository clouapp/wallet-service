package providers

import (
	"github.com/goravel/framework/contracts/foundation"
	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/activity"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/app/services/features"
	"github.com/macrowallets/waas/app/services/ramps"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/app/services/sweep"
	"github.com/macrowallets/waas/app/services/withdraw"
)

type RampServiceProvider struct{}

func (*RampServiceProvider) Register(app foundation.Application) {
	app.Singleton((*repositories.RampCustomers)(nil), func(foundation.Application) (any, error) { return repositories.NewRampCustomers(), nil })
	app.Singleton((*repositories.RampState)(nil), func(foundation.Application) (any, error) { return repositories.NewRampState(), nil })
	app.Singleton((*ramps.Tasks)(nil), func(app foundation.Application) (any, error) {
		state, err := resolve[*repositories.RampState](app)
		if err != nil {
			return nil, err
		}
		return &ramps.Tasks{Store: state}, nil
	})
	app.Singleton((*ramps.Service)(nil), func(app foundation.Application) (any, error) {
		settings, err := resolve[*settings.Service](app)
		if err != nil {
			return nil, err
		}
		features, err := resolve[*features.Service](app)
		if err != nil {
			return nil, err
		}
		customers, err := resolve[*repositories.RampCustomers](app)
		if err != nil {
			return nil, err
		}
		tasks, err := resolve[*ramps.Tasks](app)
		if err != nil {
			return nil, err
		}
		verifier, err := resolve[*authsvc.SecondFactorVerifier](app)
		if err != nil {
			return nil, err
		}
		audit, err := resolve[*repositories.AccountActivityRepository](app)
		if err != nil {
			return nil, err
		}
		wallets, err := resolve[*repositories.WalletRepository](app)
		if err != nil {
			return nil, err
		}
		addresses, err := resolve[*repositories.AddressRepository](app)
		if err != nil {
			return nil, err
		}
		chains, err := resolve[*repositories.ChainRepository](app)
		if err != nil {
			return nil, err
		}
		withdrawal, err := resolve[*withdraw.Service](app)
		if err != nil {
			return nil, err
		}
		custody, err := resolve[*sweep.Box](app)
		if err != nil {
			return nil, err
		}
		state, err := resolve[*repositories.RampState](app)
		if err != nil {
			return nil, err
		}
		baseURLs := map[string]string{}
		for _, code := range []string{"bridge", "ozav", "blindpay"} {
			baseURLs[code] = facades.Config().GetString("ramps." + code + "_base_url")
		}
		return ramps.NewService(ramps.Deps{State: state, AppEnvironment: facades.Config().GetString("app.env"), ProviderBaseURLs: baseURLs, IntentMaterialRetentionHours: facades.Config().GetInt("ramps.kyc_intent_material_retention_hours", 720), Decrypt: facades.Crypt().DecryptString, Settings: settings, Features: features, Customers: customers, Tasks: tasks, Verifier: verifier, Activity: activity.Writer(audit), Wallets: wallets, Addresses: addresses, Chains: chains, Withdraw: withdrawal, Sweep: custody}), nil
	})
}
func (*RampServiceProvider) Boot(foundation.Application) {}
