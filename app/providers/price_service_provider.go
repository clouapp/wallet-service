package providers

import (
	"context"

	"github.com/goravel/framework/contracts/foundation"

	coinapiws "github.com/macrowallets/waas/app/adapters/price/coinapi"

	// Link the CoinGecko HTTP client. Quotes still call price.NewCoinGeckoProvider.
	_ "github.com/macrowallets/waas/app/adapters/price/coingecko"
	// Link the CoinMarketCap HTTP client. Quotes still call price.NewCoinMarketCapProvider.
	_ "github.com/macrowallets/waas/app/adapters/price/coinmarketcap"
	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/price"
	"github.com/macrowallets/waas/app/services/settings"
)

// PriceServiceProvider binds the price service and the environment CoinAPI
// key the price websocket command dials with.
type PriceServiceProvider struct{}

func (p *PriceServiceProvider) Register(app foundation.Application) {
	app.Singleton((*price.CoinAPICredential)(nil), func(foundation.Application) (any, error) {
		return &price.CoinAPICredential{Key: facades.Config().GetString("vault.price.coinapi_key")}, nil
	})
	app.Singleton((*price.Service)(nil), func(app foundation.Application) (any, error) {
		return newPriceService(app)
	})
}

func (p *PriceServiceProvider) Boot(foundation.Application) {}

// newPriceService quotes through price.SettingsSource on each refresh.
// Provider keys are opened then, not copied into clients at boot. When no
// settings provider is usable, the quote keeps the environment CoinAPI key.
func newPriceService(app foundation.Application) (*price.Service, error) {
	currencies, err := resolve[*repositories.CurrencyRepository](app)
	if err != nil {
		return nil, err
	}
	credential, err := resolve[*price.CoinAPICredential](app)
	if err != nil {
		return nil, err
	}
	accountSettings, err := resolve[*settings.Service](app)
	if err != nil {
		return nil, err
	}
	return price.NewService(price.Deps{
		Currencies: currencies,
		Cache:      facades.Cache(),
	}).
		WithQuoteDialer(coinapiws.Dialer{}).
		WithEnvCoinAPIKey(credential.Key).
		WithSettingsSource(func(ctx context.Context) ([]price.Credential, error) {
			opened, err := accountSettings.PriceProvidersForQuote(ctx)
			if err != nil {
				return nil, err
			}
			credentials := make([]price.Credential, 0, len(opened))
			for _, item := range opened {
				credentials = append(credentials, price.Credential{Name: item.Name, Key: item.Key})
			}
			return credentials, nil
		}), nil
}
