package price

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var coinAPIAssetMapping = map[string]string{
	"MATIC": "POL",
}

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
	client  *http.Client
}

func NewCoinAPIProvider(apiKey string) *CoinAPIProvider {
	return &CoinAPIProvider{
		apiKey:  apiKey,
		baseURL: "https://rest.coinapi.io/v1",
		client:  &http.Client{Timeout: 15 * time.Second},
	}
}

func (p *CoinAPIProvider) Name() string { return "coinapi" }

func (p *CoinAPIProvider) FetchCryptoPrices(codes []string) (map[string]float64, error) {
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
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-CoinAPI-Key", p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("coinapi: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result struct {
		Rates []struct {
			AssetIDQuote string  `json:"asset_id_quote"`
			Rate         float64 `json:"rate"`
		} `json:"rates"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("coinapi parse: %w", err)
	}

	prices := make(map[string]float64, len(result.Rates))
	for _, rate := range result.Rates {
		code := rate.AssetIDQuote
		if reversed, ok := coinAPIReverseMapping[code]; ok {
			code = reversed
		}
		if rate.Rate > 0 {
			prices[code] = rate.Rate
		}
	}
	return prices, nil
}

func (p *CoinAPIProvider) FetchFiatRates(codes []string) (map[string]float64, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("coinapi: api key not configured")
	}

	url := fmt.Sprintf("%s/exchangerate/USD?invert=true&filter_asset_id=%s", p.baseURL, strings.Join(codes, ","))
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-CoinAPI-Key", p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("coinapi fiat: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result struct {
		Rates []struct {
			AssetIDQuote string  `json:"asset_id_quote"`
			Rate         float64 `json:"rate"`
		} `json:"rates"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("coinapi fiat parse: %w", err)
	}

	rates := make(map[string]float64, len(result.Rates))
	for _, rate := range result.Rates {
		if rate.Rate > 0 {
			rates[rate.AssetIDQuote] = 1.0 / rate.Rate
		}
	}
	return rates, nil
}
