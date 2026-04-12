package price

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var geckoIDMap = map[string]string{
	"BTC": "bitcoin", "ETH": "ethereum", "SOL": "solana", "MATIC": "matic-network",
	"LTC": "litecoin", "DOGE": "dogecoin", "USDT": "tether", "USDC": "usd-coin",
	"XRP": "ripple", "BNB": "binancecoin", "TRX": "tron", "ADA": "cardano",
	"DOT": "polkadot", "LINK": "chainlink", "AVAX": "avalanche-2", "BCH": "bitcoin-cash",
	"DAI": "dai", "TON": "the-open-network", "SHIB": "shiba-inu",
}

var geckoReverseMap map[string]string

func init() {
	geckoReverseMap = make(map[string]string, len(geckoIDMap))
	for code, id := range geckoIDMap {
		geckoReverseMap[id] = code
	}
}

type CoinGeckoProvider struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

func NewCoinGeckoProvider(apiKey string) *CoinGeckoProvider {
	baseURL := "https://api.coingecko.com/api/v3"
	if apiKey != "" {
		baseURL = "https://pro-api.coingecko.com/api/v3"
	}
	return &CoinGeckoProvider{
		apiKey:  apiKey,
		baseURL: baseURL,
		client:  &http.Client{Timeout: 15 * time.Second},
	}
}

func (p *CoinGeckoProvider) Name() string { return "coingecko" }

func (p *CoinGeckoProvider) FetchCryptoPrices(codes []string) (map[string]float64, error) {
	ids := make([]string, 0, len(codes))
	for _, code := range codes {
		if id, ok := geckoIDMap[strings.ToUpper(code)]; ok {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return map[string]float64{}, nil
	}

	url := fmt.Sprintf("%s/simple/price?ids=%s&vs_currencies=usd", p.baseURL, strings.Join(ids, ","))
	body, err := p.doGet(url)
	if err != nil {
		return nil, fmt.Errorf("coingecko crypto prices: %w", err)
	}

	var result map[string]map[string]float64
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("coingecko parse: %w", err)
	}

	prices := make(map[string]float64, len(result))
	for geckoID, data := range result {
		if code, ok := geckoReverseMap[geckoID]; ok {
			if usdPrice, exists := data["usd"]; exists && usdPrice > 0 {
				prices[code] = usdPrice
			}
		}
	}
	return prices, nil
}

func (p *CoinGeckoProvider) FetchFiatRates(codes []string) (map[string]float64, error) {
	lowerCodes := make([]string, len(codes))
	for i, c := range codes {
		lowerCodes[i] = strings.ToLower(c)
	}

	url := fmt.Sprintf("%s/simple/price?ids=usd-coin&vs_currencies=%s", p.baseURL, strings.Join(lowerCodes, ","))
	body, err := p.doGet(url)
	if err != nil {
		return nil, fmt.Errorf("coingecko fiat rates: %w", err)
	}

	var result map[string]map[string]float64
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("coingecko fiat parse: %w", err)
	}

	rates := make(map[string]float64, len(codes))
	if usdcData, ok := result["usd-coin"]; ok {
		for _, code := range codes {
			lower := strings.ToLower(code)
			if fiatVal, exists := usdcData[lower]; exists && fiatVal > 0 {
				rates[code] = 1.0 / fiatVal
			}
		}
	}
	return rates, nil
}

func (p *CoinGeckoProvider) doGet(url string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	if p.apiKey != "" {
		req.Header.Set("x-cg-pro-api-key", p.apiKey)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}
	return io.ReadAll(resp.Body)
}
