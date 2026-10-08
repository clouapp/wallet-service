package coinapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/price"
	"github.com/macrowallets/waas/pkg/httpclient"
)

var coinAPIAssetMapping = map[string]string{}

var coinAPIReverseMapping map[string]string

func init() {
	coinAPIReverseMapping = make(map[string]string, len(coinAPIAssetMapping))
	for code, asset := range coinAPIAssetMapping {
		coinAPIReverseMapping[asset] = code
	}
}

const (
	restBaseURL = "https://rest.coinapi.io/v1"
	httpTimeout = 15 * time.Second
)

// CoinAPIProvider calls the CoinAPI REST exchangerate API.
type CoinAPIProvider struct {
	apiKey  string
	baseURL string
	client  *httpclient.Client
}

// NewCoinAPIProvider returns a REST quote client. An empty apiKey still builds
// the client; each fetch reports that the key is not configured.
func NewCoinAPIProvider(apiKey string) *CoinAPIProvider {
	return &CoinAPIProvider{
		apiKey:  apiKey,
		baseURL: restBaseURL,
		client:  httpclient.NewClient(httpTimeout),
	}
}

// Name is the provider id stored with a quote.
func (p *CoinAPIProvider) Name() string { return "coinapi" }

// FetchCryptoPrices returns USD prices. Codes are uppercased unless the asset
// map renames them. A non-positive rate is skipped.
func (p *CoinAPIProvider) FetchCryptoPrices(ctx context.Context, codes []string) (map[string]decimal.Decimal, error) {
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
	body, err := p.get(ctx, url, "coinapi")
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

// FetchFiatRates returns USD per fiat unit. The exchangerate payload is units
// per USD, and the price column stores the inverted USD rate.
func (p *CoinAPIProvider) FetchFiatRates(ctx context.Context, codes []string) (map[string]decimal.Decimal, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("coinapi: api key not configured")
	}

	url := fmt.Sprintf("%s/exchangerate/USD?invert=true&filter_asset_id=%s", p.baseURL, strings.Join(codes, ","))
	body, err := p.get(ctx, url, "coinapi fiat")
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
			rates[rate.AssetIDQuote] = price.InvertRate(rate.Rate)
		}
	}
	return rates, nil
}

func (p *CoinAPIProvider) get(ctx context.Context, url, label string) ([]byte, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%s: context is required", label)
	}
	ctx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()

	resp, err := p.client.Do(ctx, httpclient.Request{
		Method: httpclient.MethodGet,
		URL:    url,
		Header: map[string]string{"X-CoinAPI-Key": p.apiKey},
	})
	if err != nil {
		if httpclient.IsBuild(err) {
			return nil, err
		}
		return nil, chain.Unavailable(fmt.Errorf("%s: %w", label, err))
	}
	return resp.Body, nil
}

var _ price.PriceProvider = (*CoinAPIProvider)(nil)
