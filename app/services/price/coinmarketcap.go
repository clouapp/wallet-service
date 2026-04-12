package price

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var cmcAssetMapping = map[string]string{
	"MATIC": "MATIC",
}

type CoinMarketCapProvider struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

func NewCoinMarketCapProvider(apiKey string) *CoinMarketCapProvider {
	return &CoinMarketCapProvider{
		apiKey:  apiKey,
		baseURL: "https://pro-api.coinmarketcap.com",
		client:  &http.Client{Timeout: 15 * time.Second},
	}
}

func (p *CoinMarketCapProvider) Name() string { return "coinmarketcap" }

func (p *CoinMarketCapProvider) FetchCryptoPrices(codes []string) (map[string]float64, error) {
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
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-CMC_PRO_API_KEY", p.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("coinmarketcap: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result struct {
		Data map[string]struct {
			Quote struct {
				USD struct {
					Price float64 `json:"price"`
				} `json:"USD"`
			} `json:"quote"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("coinmarketcap parse: %w", err)
	}

	prices := make(map[string]float64, len(result.Data))
	for apiSym, data := range result.Data {
		if code, ok := reverseMap[apiSym]; ok && data.Quote.USD.Price > 0 {
			prices[code] = data.Quote.USD.Price
		}
	}
	return prices, nil
}

func (p *CoinMarketCapProvider) FetchFiatRates(codes []string) (map[string]float64, error) {
	return nil, fmt.Errorf("coinmarketcap: fiat rates not supported")
}
