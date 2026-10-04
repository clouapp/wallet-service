package price

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/macrowallets/waas/pkg/httpclient"
	"github.com/shopspring/decimal"
)

var cmcAssetMapping = map[string]string{
	"MATIC": "MATIC",
}

type CoinMarketCapProvider struct {
	apiKey  string
	baseURL string
	client  *httpclient.Client
}

func NewCoinMarketCapProvider(apiKey string) *CoinMarketCapProvider {
	return &CoinMarketCapProvider{
		apiKey:  apiKey,
		baseURL: "https://pro-api.coinmarketcap.com",
		client:  httpclient.NewClient(15 * time.Second),
	}
}

func (p *CoinMarketCapProvider) Name() string { return "coinmarketcap" }

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
	resp, err := p.client.Do(context.Background(), httpclient.Request{
		Method: httpclient.MethodGet,
		URL:    url,
		Header: map[string]string{
			"X-CMC_PRO_API_KEY": p.apiKey,
			"Accept":            "application/json",
		},
	})
	if err != nil {
		if httpclient.IsBuild(err) {
			return nil, err
		}
		if httpclient.IsRead(err) {
			return nil, err
		}
		return nil, fmt.Errorf("coinmarketcap: %w", err)
	}
	body := resp.Body

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

func (p *CoinMarketCapProvider) FetchFiatRates(codes []string) (map[string]decimal.Decimal, error) {
	return nil, fmt.Errorf("coinmarketcap: fiat rates not supported")
}
