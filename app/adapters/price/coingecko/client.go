package coingecko

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

var geckoIDMap = map[string]string{
	"BTC": "bitcoin", "ETH": "ethereum", "SOL": "solana", "MATIC": "matic-network", "POL": "polygon-ecosystem-token",
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

const (
	publicBaseURL = "https://api.coingecko.com/api/v3"
	proBaseURL    = "https://pro-api.coingecko.com/api/v3"
	httpTimeout   = 15 * time.Second
)

// CoinGeckoProvider calls the CoinGecko simple/price API.
type CoinGeckoProvider struct {
	apiKey  string
	baseURL string
	client  *httpclient.Client
}

// NewCoinGeckoProvider returns a provider. An empty apiKey uses the public host.
// A non-empty key uses the pro host and is sent as x-cg-pro-api-key.
func NewCoinGeckoProvider(apiKey string) *CoinGeckoProvider {
	baseURL := publicBaseURL
	if apiKey != "" {
		baseURL = proBaseURL
	}
	return &CoinGeckoProvider{
		apiKey:  apiKey,
		baseURL: baseURL,
		client:  httpclient.NewClient(httpTimeout),
	}
}

// Name is the provider id stored with a quote.
func (p *CoinGeckoProvider) Name() string { return "coingecko" }

// FetchCryptoPrices returns USD prices for the codes CoinGecko knows.
// Unknown codes are skipped. A non-positive price is skipped.
func (p *CoinGeckoProvider) FetchCryptoPrices(codes []string) (map[string]decimal.Decimal, error) {
	ids := make([]string, 0, len(codes))
	for _, code := range codes {
		if id, ok := geckoIDMap[strings.ToUpper(code)]; ok {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return map[string]decimal.Decimal{}, nil
	}

	url := fmt.Sprintf("%s/simple/price?ids=%s&vs_currencies=usd", p.baseURL, strings.Join(ids, ","))
	body, err := p.doGet(url)
	if err != nil {
		return nil, fmt.Errorf("coingecko crypto prices: %w", err)
	}

	var result map[string]map[string]decimal.Decimal
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("coingecko parse: %w", err)
	}

	prices := make(map[string]decimal.Decimal, len(result))
	for geckoID, data := range result {
		if code, ok := geckoReverseMap[geckoID]; ok {
			if usdPrice, exists := data["usd"]; exists && usdPrice.IsPositive() {
				prices[code] = usdPrice
			}
		}
	}
	return prices, nil
}

// FetchFiatRates returns USD per fiat unit. CoinGecko quotes fiat per USDC,
// and the price column stores the inverted USD rate.
func (p *CoinGeckoProvider) FetchFiatRates(codes []string) (map[string]decimal.Decimal, error) {
	lowerCodes := make([]string, len(codes))
	for i, c := range codes {
		lowerCodes[i] = strings.ToLower(c)
	}

	url := fmt.Sprintf("%s/simple/price?ids=usd-coin&vs_currencies=%s", p.baseURL, strings.Join(lowerCodes, ","))
	body, err := p.doGet(url)
	if err != nil {
		return nil, fmt.Errorf("coingecko fiat rates: %w", err)
	}

	var result map[string]map[string]decimal.Decimal
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("coingecko fiat parse: %w", err)
	}

	rates := make(map[string]decimal.Decimal, len(codes))
	if usdcData, ok := result["usd-coin"]; ok {
		for _, code := range codes {
			lower := strings.ToLower(code)
			if fiatVal, exists := usdcData[lower]; exists && fiatVal.IsPositive() {
				rates[code] = price.InvertRate(fiatVal)
			}
		}
	}
	return rates, nil
}

func (p *CoinGeckoProvider) doGet(url string) ([]byte, error) {
	header := map[string]string{"Accept": "application/json"}
	if p.apiKey != "" {
		header["x-cg-pro-api-key"] = p.apiKey
	}
	resp, err := p.client.Do(context.Background(), httpclient.Request{
		Method: httpclient.MethodGet,
		URL:    url,
		Header: header,
	})
	if err != nil {
		if httpclient.IsBuild(err) {
			return nil, err
		}
		return nil, chain.Unavailable(err)
	}
	if resp.StatusCode != httpclient.StatusOK {
		return nil, chain.FromProviderHTTP(resp.StatusCode, httpclient.RedactURLText(string(resp.Body), url))
	}
	return resp.Body, nil
}

var _ price.PriceProvider = (*CoinGeckoProvider)(nil)
