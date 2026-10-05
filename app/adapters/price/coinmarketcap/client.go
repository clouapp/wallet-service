package coinmarketcap

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/app/services/price"
	"github.com/macrowallets/waas/pkg/httpclient"
)

var cmcAssetMapping = map[string]string{
	"MATIC": "MATIC",
}

const (
	restBaseURL = "https://pro-api.coinmarketcap.com"
	httpTimeout = 15 * time.Second
)

// CoinMarketCapProvider calls the CoinMarketCap quotes/latest API.
type CoinMarketCapProvider struct {
	apiKey  string
	baseURL string
	client  *httpclient.Client
}

// NewCoinMarketCapProvider returns a REST quote client. An empty apiKey still builds
// the client; each crypto fetch reports that the key is not configured.
func NewCoinMarketCapProvider(apiKey string) *CoinMarketCapProvider {
	return &CoinMarketCapProvider{
		apiKey:  apiKey,
		baseURL: restBaseURL,
		client:  httpclient.NewClient(httpTimeout),
	}
}

// Name is the provider id stored with a quote.
func (p *CoinMarketCapProvider) Name() string { return "coinmarketcap" }

// FetchCryptoPrices returns USD prices. Codes are uppercased unless the asset
// map renames them. A non-positive price, or a symbol that was not requested, is skipped.
func (p *CoinMarketCapProvider) FetchCryptoPrices(codes []string) (map[string]decimal.Decimal, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("coinmarketcap: api key not configured")
	}

	apiSymbols := make([]string, len(codes))
	reverseMap := make(map[string]string, len(codes))
	for i, code := range codes {
		upper := strings.ToUpper(code)
		apiSym := upper
		if mapped, ok := cmcAssetMapping[upper]; ok {
			apiSym = mapped
		}
		apiSymbols[i] = apiSym
		reverseMap[apiSym] = upper
	}

	url := fmt.Sprintf("%s/v1/cryptocurrency/quotes/latest?symbol=%s&convert=USD", p.baseURL, strings.Join(apiSymbols, ","))
	body, err := p.get(url)
	if err != nil {
		return nil, err
	}

	var result struct {
		Data map[string]struct {
			Quote struct {
				USD struct {
					Price decimal.Decimal `json:"price"`
				} `json:"USD"`
			} `json:"quote"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("coinmarketcap parse: %w", err)
	}

	prices := make(map[string]decimal.Decimal, len(result.Data))
	for apiSym, data := range result.Data {
		if code, ok := reverseMap[apiSym]; ok && data.Quote.USD.Price.IsPositive() {
			prices[code] = data.Quote.USD.Price
		}
	}
	return prices, nil
}

// FetchFiatRates reports that CoinMarketCap fiat quotes are not used.
func (p *CoinMarketCapProvider) FetchFiatRates(codes []string) (map[string]decimal.Decimal, error) {
	return nil, fmt.Errorf("coinmarketcap: fiat rates not supported")
}

func (p *CoinMarketCapProvider) get(url string) ([]byte, error) {
	resp, err := p.client.Do(context.Background(), httpclient.Request{
		Method: httpclient.MethodGet,
		URL:    url,
		Header: map[string]string{
			"X-CMC_PRO_API_KEY": p.apiKey,
			"Accept":            "application/json",
		},
	})
	if err != nil {
		if httpclient.IsBuild(err) || httpclient.IsRead(err) {
			return nil, err
		}
		return nil, fmt.Errorf("coinmarketcap: %w", err)
	}
	return resp.Body, nil
}

var _ price.PriceProvider = (*CoinMarketCapProvider)(nil)
