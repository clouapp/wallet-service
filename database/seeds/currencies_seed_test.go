package seeds_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/database/seeds"
	"github.com/macrowallets/waas/pkg/numeric"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

func TestCurrencies_Seed_DoesNotQueryOutsideTheRepository(t *testing.T) {
	source, err := os.ReadFile("currencies.go")
	if err != nil {
		t.Fatalf("read currencies seed: %v", err)
	}
	text := string(source)
	for _, needle := range []string{"facades.", "Orm()", ".Query()", ".Exec(", ".Raw("} {
		if strings.Contains(text, needle) {
			t.Fatalf("currencies seed still queries outside the repository (%s)", needle)
		}
	}
}

func TestSeed_Currencies_InsertsTheCatalogAndLeavesExistingRowsOnRerun(t *testing.T) {
	fixtures.TestDB(t)
	ctx := context.Background()
	if err := seeds.SeedCurrencies(ctx); err != nil {
		t.Fatalf("seed currencies: %v", err)
	}

	currencies := repositories.NewCurrencyRepository(nil)
	assertSeededCurrencyCatalog(t, ctx, currencies)

	btc, err := currencies.FindByCode(ctx, "BTC")
	if err != nil {
		t.Fatalf("find bitcoin: %v", err)
	}
	usd, err := currencies.FindByCode(ctx, "USD")
	if err != nil {
		t.Fatalf("find dollar: %v", err)
	}
	btcID, usdID := btc.ID, usd.ID

	if err := currencies.SetPrice(ctx, "BTC", decimal.NewFromInt(42), decimal.NewFromInt(41)); err != nil {
		t.Fatalf("drift bitcoin price: %v", err)
	}
	if err := seeds.SeedCurrencies(ctx); err != nil {
		t.Fatalf("reseed currencies: %v", err)
	}

	btc, err = currencies.FindByCode(ctx, "BTC")
	if err != nil {
		t.Fatalf("find bitcoin after reseed: %v", err)
	}
	if btc.ID != btcID || btc.Name != "Bitcoin" || !btc.Active || btc.Subunits != 8 {
		t.Fatalf("reseed rewrote bitcoin: id=%s name=%q active=%v subunits=%d", btc.ID, btc.Name, btc.Active, btc.Subunits)
	}
	if !btc.CurrentPrice.Equal(decimal.NewFromInt(42)) || !btc.LastPrice.Valid || !btc.LastPrice.Decimal.Equal(decimal.NewFromInt(41)) {
		t.Fatalf("reseed refreshed the bitcoin price")
	}
	usd, err = currencies.FindByCode(ctx, "USD")
	if err != nil {
		t.Fatalf("find dollar after reseed: %v", err)
	}
	if usd.ID != usdID || !usd.CurrentPrice.Equal(decimal.NewFromInt(1)) || !usd.Active {
		t.Fatalf("reseed rewrote the dollar")
	}
}

func TestSeed_Currencies_KeepsADisabledRow(t *testing.T) {
	fixtures.TestDB(t)
	ctx := context.Background()
	currencies := repositories.NewCurrencyRepository(nil)
	existingID := uuid.MustParse("00000000-0000-0000-0000-0000000000c1")
	if err := currencies.Create(ctx, &models.Currency{
		ID:           existingID,
		Name:         "Old Bitcoin",
		Code:         "BTC",
		Symbol:       "OLD",
		Type:         models.CurrencyTypeCrypto,
		Subunits:     4,
		CurrentPrice: numeric.NewDecimal(decimal.NewFromInt(9)),
		Active:       false,
	}); err != nil {
		t.Fatalf("create disabled bitcoin: %v", err)
	}

	if err := seeds.SeedCurrencies(ctx); err != nil {
		t.Fatalf("seed currencies: %v", err)
	}

	btc, err := currencies.FindByCode(ctx, "BTC")
	if err != nil {
		t.Fatalf("find disabled bitcoin: %v", err)
	}
	if btc.ID != existingID || btc.Name != "Old Bitcoin" || btc.Symbol != "OLD" || btc.Active || btc.Subunits != 4 {
		t.Fatalf("seed replaced a disabled bitcoin: id=%s name=%q symbol=%q active=%v subunits=%d", btc.ID, btc.Name, btc.Symbol, btc.Active, btc.Subunits)
	}
	if !btc.CurrentPrice.Equal(decimal.NewFromInt(9)) {
		t.Fatal("seed refreshed the price of a disabled bitcoin")
	}
	eth, err := currencies.FindByCode(ctx, "ETH")
	if err != nil {
		t.Fatalf("find ethereum: %v", err)
	}
	if eth.Name != "Ethereum" || !eth.Active || eth.Subunits != 18 || eth.Type != models.CurrencyTypeCrypto {
		t.Fatalf("ethereum was not seeded beside the disabled bitcoin")
	}

	if err := seeds.SeedCurrencies(ctx); err != nil {
		t.Fatalf("reseed currencies: %v", err)
	}
	btc, err = currencies.FindByCode(ctx, "BTC")
	if err != nil {
		t.Fatalf("find disabled bitcoin after reseed: %v", err)
	}
	if btc.ID != existingID || btc.Name != "Old Bitcoin" || btc.Active {
		t.Fatal("reseed replaced a disabled bitcoin")
	}
}

func assertSeededCurrencyCatalog(t *testing.T, ctx context.Context, currencies *repositories.CurrencyRepository) {
	t.Helper()
	const cmcBase = "https://s2.coinmarketcap.com/static/img/coins/64x64"
	cryptos := []struct {
		code, name, symbol, logo string
		subunits                 int
	}{
		{"BTC", "Bitcoin", "₿", cmcBase + "/1.png", 8},
		{"ETH", "Ethereum", "Ξ", cmcBase + "/1027.png", 18},
		{"SOL", "Solana", "◎", cmcBase + "/5426.png", 9},
		{"POL", "Polygon", "POL", cmcBase + "/3890.png", 18},
		{"LTC", "Litecoin", "Ł", cmcBase + "/2.png", 8},
		{"DOGE", "Dogecoin", "Ð", cmcBase + "/74.png", 8},
		{"USDT", "Tether", "₮", cmcBase + "/825.png", 6},
		{"USDC", "USD Coin", "USDC", cmcBase + "/3408.png", 6},
		{"XRP", "XRP", "✕", cmcBase + "/52.png", 6},
		{"BNB", "BNB", "BNB", cmcBase + "/1839.png", 18},
		{"TRX", "TRON", "TRX", cmcBase + "/1958.png", 6},
		{"ADA", "Cardano", "₳", cmcBase + "/2010.png", 6},
		{"DOT", "Polkadot", "DOT", cmcBase + "/6636.png", 10},
		{"LINK", "Chainlink", "LINK", cmcBase + "/1975.png", 18},
		{"AVAX", "Avalanche", "AVAX", cmcBase + "/5805.png", 18},
		{"BCH", "Bitcoin Cash", "BCH", cmcBase + "/1831.png", 8},
		{"DAI", "Dai", "DAI", cmcBase + "/4943.png", 18},
		{"TON", "Toncoin", "TON", cmcBase + "/11419.png", 9},
		{"SHIB", "Shiba Inu", "SHIB", cmcBase + "/5994.png", 18},
	}
	for _, want := range cryptos {
		found := requireCurrency(t, ctx, currencies, want.code)
		if found.Name != want.name || found.Symbol != want.symbol || found.Type != models.CurrencyTypeCrypto || !found.Active || found.Subunits != want.subunits {
			t.Fatalf("crypto %s: name=%q symbol=%q type=%q active=%v subunits=%d", want.code, found.Name, found.Symbol, found.Type, found.Active, found.Subunits)
		}
		if found.Logo == nil || *found.Logo != want.logo {
			t.Fatalf("crypto %s logo was not seeded", want.code)
		}
		requireDefaultPrice(t, found)
	}

	activeFiats := map[string]bool{
		"USD": true, "EUR": true, "BRL": true, "GBP": true, "JPY": true,
		"CAD": true, "AUD": true, "CHF": true, "CNY": true, "INR": true,
		"IDR": true, "KRW": true, "MXN": true, "DKK": true, "NZD": true,
		"PHP": true, "RUB": true, "PEN": true, "PLN": true, "VND": true,
		"TRY": true, "ARS": true, "NGN": true,
	}
	// Three-decimal fiats. A seed subunits of 0 is omitted on insert, the same
	// way a zero price is, so those codes keep the column default of 2.
	fiatSubunits := map[string]int{
		"BHD": 3, "IQD": 3, "JOD": 3, "KWD": 3, "LYD": 3, "OMR": 3, "TND": 3,
	}
	fiatCodes := strings.Fields("USD EUR BRL GBP JPY CAD AUD CHF CNY INR IDR KRW MXN DKK NZD PHP RUB PEN PLN VND TRY ARS NGN AFN ALL DZD AOA AMD AWG AZN BSD BHD BDT BBD BYN BZD BMD BOB BAM BWP BGN BIF KHR CVE CLP COP KMF CDF CRC HRK CUP CZK DJF DOP XCD EGP ERN ETB FJD GEL GHS GTQ GNF GYD HTG HNL HKD HUF ISK IRR IQD ILS JMD JOD KZT KES KWD KGS LAK LBP LSL LRD LYD MOP MGA MWK MYR MVR MRU MUR MDL MNT MAD MZN MMK NAD NPR NIO KPW NOK OMR PKR PAB PGK PYG QAR RON RWF SAR RSD SCR SLL SGD SBD SOS ZAR SSP LKR SDG SRD SZL SEK SYP TWD TJS TZS THB TOP TTD TND TMT UGX UAH AED UYU UZS VUV VES XOF XAF XPF WST YER ZMW")
	if len(fiatCodes) != 144 {
		t.Fatalf("fiat catalog has %d codes", len(fiatCodes))
	}
	for _, code := range fiatCodes {
		found := requireCurrency(t, ctx, currencies, code)
		if found.Type != models.CurrencyTypeFiat || found.Logo != nil {
			t.Fatalf("fiat %s: type=%q logo=%v", code, found.Type, found.Logo)
		}
		if found.Active != activeFiats[code] {
			t.Fatalf("fiat %s active=%v", code, found.Active)
		}
		wantSubunits := 2
		if subunits, ok := fiatSubunits[code]; ok {
			wantSubunits = subunits
		}
		if found.Subunits != wantSubunits {
			t.Fatalf("fiat %s subunits=%d, want %d", code, found.Subunits, wantSubunits)
		}
		requireDefaultPrice(t, found)
	}

	activeCryptos, err := currencies.FindActiveCryptos(ctx)
	if err != nil {
		t.Fatalf("list active cryptos: %v", err)
	}
	if len(activeCryptos) != len(cryptos) {
		t.Fatalf("active cryptos = %d, want %d", len(activeCryptos), len(cryptos))
	}
	activeFiatRows, err := currencies.FindActiveFiats(ctx)
	if err != nil {
		t.Fatalf("list active fiats: %v", err)
	}
	if len(activeFiatRows) != len(activeFiats) {
		t.Fatalf("active fiats = %d, want %d", len(activeFiatRows), len(activeFiats))
	}
}

func requireCurrency(t *testing.T, ctx context.Context, currencies *repositories.CurrencyRepository, code string) *models.Currency {
	t.Helper()
	found, err := currencies.FindByCode(ctx, code)
	if err != nil {
		t.Fatalf("find currency %s: %v", code, err)
	}
	return found
}

func requireDefaultPrice(t *testing.T, found *models.Currency) {
	t.Helper()
	if !found.CurrentPrice.Equal(decimal.NewFromInt(1)) || found.LastPrice.Valid || found.PriceUpdatedAt != nil {
		t.Fatalf("currency %s price was not the column default", found.Code)
	}
}
