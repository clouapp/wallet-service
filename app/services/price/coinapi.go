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

var coinAPIAssetMapping = map[string]string{}

var coinAPIReverseMapping map[string]string

func init() {
	coinAPIReverseMapping = make(map[string]string, len(coinAPIAssetMapping))
	for code, asset := range coinAPIAssetMapping {
		coinAPIReverseMapping[asset] = code
	}
}

type CoinAPIProvider struct {
	apiKey  string
	baseURL string
	client  *httpclient.Client
}

func NewCoinAPIProvider(apiKey string) *CoinAPIProvider {
	return &CoinAPIProvider{
		apiKey:  apiKey,
		baseURL: "https://rest.coinapi.io/v1",
		client:  httpclient.NewClient(15 * time.Second),
	}
}

func (p *CoinAPIProvider) Name() string { return "coinapi" }

func (p *CoinAPIProvider) FetchCryptoPrices(codes []string) (map[string]decimal.Decimal, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("coinapi: api key not configured")
	}

	apiCodes := make([]string, len(codes))
	for i, code := range codes {
		upper := strings.ToUpper(code)
		if mapped, ok := coinAPIAssetMapping[upper]; ok {
			apiCodes[i] = mapped
		} else {
			apiCodes[i] = upper
		}
	}

	url := fmt.Sprintf("%s/exchangerate/USD?invert=true&filter_asset_id=%s", p.baseURL, strings.Join(apiCodes, ","))
	body, err := p.get(url, "coinapi")
	if err != nil {
		return nil, err
	}

	var result struct {
		Rates []struct {
			AssetIDQuote string          `json:"asset_id_quote"`
			Rate         decimal.Decimal `json:"rate"`
		} `json:"rates"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("coinapi parse: %w", err)
	}

	prices := make(map[string]decimal.Decimal, len(result.Rates))
	for _, rate := range result.Rates {
		code := rate.AssetIDQuote
		if reversed, ok := coinAPIReverseMapping[code]; ok {
			code = reversed
		}
		if rate.Rate.IsPositive() {
			prices[code] = rate.Rate
		}
	}
	return prices, nil
}

func (p *CoinAPIProvider) FetchFiatRates(codes []string) (map[string]decimal.Decimal, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("coinapi: api key not configured")
	}

	url := fmt.Sprintf("%s/exchangerate/USD?invert=true&filter_asset_id=%s", p.baseURL, strings.Join(codes, ","))
	body, err := p.get(url, "coinapi fiat")
	if err != nil {
		return nil, err
	}

	var result struct {
		Rates []struct {
			AssetIDQuote string          `json:"asset_id_quote"`
			Rate         decimal.Decimal `json:"rate"`
		} `json:"rates"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("coinapi fiat parse: %w", err)
	}

	rates := make(map[string]decimal.Decimal, len(result.Rates))
	for _, rate := range result.Rates {
		if rate.Rate.IsPositive() {
			rates[rate.AssetIDQuote] = invertRate(rate.Rate)
		}
	}
	return rates, nil
}

func (p *CoinAPIProvider) get(url, label string) ([]byte, error) {
	resp, err := p.client.Do(context.Background(), httpclient.Request{
		Method: httpclient.MethodGet,
		URL:    url,
		Header: map[string]string{"X-CoinAPI-Key": p.apiKey},
	})
	if err != nil {
		if httpclient.IsBuild(err) || httpclient.IsRead(err) {
			return nil, err
		}
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	return resp.Body, nil
}
