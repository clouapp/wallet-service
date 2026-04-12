package seeds

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

type currencySeed struct {
	Name     string
	Code     string
	Symbol   string
	Subunits int
	Active   bool
	Logo     string
}

var activeFiatCodes = map[string]bool{
	"USD": true, "EUR": true, "BRL": true, "GBP": true, "JPY": true,
	"CAD": true, "AUD": true, "CHF": true, "CNY": true, "INR": true,
	"IDR": true, "KRW": true, "MXN": true, "DKK": true, "NZD": true,
	"PHP": true, "RUB": true, "PEN": true, "PLN": true, "VND": true,
	"TRY": true, "ARS": true, "NGN": true,
}

func SeedCurrencies(_ context.Context) error {
	if err := seedCryptos(); err != nil {
		return err
	}
	return seedFiats()
}

func seedCryptos() error {
	const cmcBase = "https://s2.coinmarketcap.com/static/img/coins/64x64"

	cryptos := []currencySeed{
		{"Bitcoin", "BTC", "₿", 8, true, cmcBase + "/1.png"},
		{"Ethereum", "ETH", "Ξ", 18, true, cmcBase + "/1027.png"},
		{"Solana", "SOL", "◎", 9, true, cmcBase + "/5426.png"},
		{"Polygon", "MATIC", "MATIC", 18, true, cmcBase + "/3890.png"},
		{"Litecoin", "LTC", "Ł", 8, true, cmcBase + "/2.png"},
		{"Dogecoin", "DOGE", "Ð", 8, true, cmcBase + "/74.png"},
		{"Tether", "USDT", "₮", 6, true, cmcBase + "/825.png"},
		{"USD Coin", "USDC", "USDC", 6, true, cmcBase + "/3408.png"},
		{"XRP", "XRP", "✕", 6, true, cmcBase + "/52.png"},
		{"BNB", "BNB", "BNB", 18, true, cmcBase + "/1839.png"},
		{"TRON", "TRX", "TRX", 6, true, cmcBase + "/1958.png"},
		{"Cardano", "ADA", "₳", 6, true, cmcBase + "/2010.png"},
		{"Polkadot", "DOT", "DOT", 10, true, cmcBase + "/6636.png"},
		{"Chainlink", "LINK", "LINK", 18, true, cmcBase + "/1975.png"},
		{"Avalanche", "AVAX", "AVAX", 18, true, cmcBase + "/5805.png"},
		{"Bitcoin Cash", "BCH", "BCH", 8, true, cmcBase + "/1831.png"},
		{"Dai", "DAI", "DAI", 18, true, cmcBase + "/4943.png"},
		{"Toncoin", "TON", "TON", 9, true, cmcBase + "/11419.png"},
		{"Shiba Inu", "SHIB", "SHIB", 18, true, cmcBase + "/5994.png"},
	}

	for _, c := range cryptos {
		var existing models.Currency
		if err := facades.Orm().Query().Where("code", c.Code).First(&existing); err == nil && existing.ID != uuid.Nil {
			slog.Info("currency already exists, skipping", "code", c.Code)
			continue
		}
		logo := c.Logo
		cur := models.Currency{
			ID:           uuid.New(),
			Name:         c.Name,
			Code:         c.Code,
			Symbol:       c.Symbol,
			Type:         models.CurrencyTypeCrypto,
			Logo:         &logo,
			Subunits:     c.Subunits,
			CurrentPrice: 0,
			Active:       c.Active,
		}
		if err := facades.Orm().Query().Create(&cur); err != nil {
			return err
		}
		slog.Info("created crypto currency", "code", c.Code)
	}
	return nil
}

func seedFiats() error {
	fiats := []currencySeed{
		{"US Dollar", "USD", "$", 2, true, ""},
		{"Euro", "EUR", "€", 2, true, ""},
		{"Brazilian Real", "BRL", "R$", 2, true, ""},
		{"British Pound", "GBP", "£", 2, false, ""},
		{"Japanese Yen", "JPY", "¥", 0, true, ""},
		{"Canadian Dollar", "CAD", "$", 2, true, ""},
		{"Australian Dollar", "AUD", "$", 2, true, ""},
		{"Swiss Franc", "CHF", "CHF", 2, false, ""},
		{"Chinese Yuan", "CNY", "¥", 2, true, ""},
		{"Indian Rupee", "INR", "₹", 2, true, ""},
		{"Indonesian Rupiah", "IDR", "Rp", 0, true, ""},
		{"South Korean Won", "KRW", "₩", 0, true, ""},
		{"Mexican Peso", "MXN", "$", 2, true, ""},
		{"Danish Krone", "DKK", "kr", 2, true, ""},
		{"New Zealand Dollar", "NZD", "$", 2, true, ""},
		{"Philippine Peso", "PHP", "₱", 2, true, ""},
		{"Russian Ruble", "RUB", "₽", 2, true, ""},
		{"Peruvian Sol", "PEN", "S/.", 2, true, ""},
		{"Polish Zloty", "PLN", "zł", 2, true, ""},
		{"Vietnamese Dong", "VND", "₫", 0, true, ""},
		{"Turkish Lira", "TRY", "₺", 2, true, ""},
		{"Argentine Peso", "ARS", "$", 2, true, ""},
		{"Nigerian Naira", "NGN", "₦", 2, true, ""},
		{"Afghan Afghani", "AFN", "؋", 2, false, ""},
		{"Albanian Lek", "ALL", "L", 2, false, ""},
		{"Algerian Dinar", "DZD", "د.ج", 2, false, ""},
		{"Angolan Kwanza", "AOA", "Kz", 2, false, ""},
		{"Armenian Dram", "AMD", "֏", 2, false, ""},
		{"Aruban Florin", "AWG", "ƒ", 2, false, ""},
		{"Azerbaijani Manat", "AZN", "₼", 2, false, ""},
		{"Bahamian Dollar", "BSD", "$", 2, false, ""},
		{"Bahraini Dinar", "BHD", ".د.ب", 3, false, ""},
		{"Bangladeshi Taka", "BDT", "৳", 2, false, ""},
		{"Barbadian Dollar", "BBD", "$", 2, false, ""},
		{"Belarusian Ruble", "BYN", "Br", 2, false, ""},
		{"Belize Dollar", "BZD", "BZ$", 2, false, ""},
		{"Bermudian Dollar", "BMD", "$", 2, false, ""},
		{"Boliviano", "BOB", "Bs.", 2, false, ""},
		{"Bosnia Mark", "BAM", "KM", 2, false, ""},
		{"Botswana Pula", "BWP", "P", 2, false, ""},
		{"Bulgarian Lev", "BGN", "лв", 2, false, ""},
		{"Burundian Franc", "BIF", "FBu", 0, false, ""},
		{"Cambodian Riel", "KHR", "៛", 2, false, ""},
		{"Cape Verdean Escudo", "CVE", "$", 2, false, ""},
		{"Chilean Peso", "CLP", "$", 0, false, ""},
		{"Colombian Peso", "COP", "$", 2, false, ""},
		{"Comorian Franc", "KMF", "CF", 0, false, ""},
		{"Congolese Franc", "CDF", "FC", 2, false, ""},
		{"Costa Rican Colon", "CRC", "₡", 2, false, ""},
		{"Croatian Kuna", "HRK", "kn", 2, false, ""},
		{"Cuban Peso", "CUP", "₱", 2, false, ""},
		{"Czech Koruna", "CZK", "Kč", 2, false, ""},
		{"Djiboutian Franc", "DJF", "Fdj", 0, false, ""},
		{"Dominican Peso", "DOP", "RD$", 2, false, ""},
		{"East Caribbean Dollar", "XCD", "$", 2, false, ""},
		{"Egyptian Pound", "EGP", "£", 2, false, ""},
		{"Eritrean Nakfa", "ERN", "Nfk", 2, false, ""},
		{"Ethiopian Birr", "ETB", "Br", 2, false, ""},
		{"Fijian Dollar", "FJD", "$", 2, false, ""},
		{"Georgian Lari", "GEL", "₾", 2, false, ""},
		{"Ghanaian Cedi", "GHS", "₵", 2, false, ""},
		{"Guatemalan Quetzal", "GTQ", "Q", 2, false, ""},
		{"Guinean Franc", "GNF", "FG", 0, false, ""},
		{"Guyanese Dollar", "GYD", "$", 2, false, ""},
		{"Haitian Gourde", "HTG", "G", 2, false, ""},
		{"Honduran Lempira", "HNL", "L", 2, false, ""},
		{"Hong Kong Dollar", "HKD", "HK$", 2, false, ""},
		{"Hungarian Forint", "HUF", "Ft", 2, false, ""},
		{"Icelandic Krona", "ISK", "kr", 0, false, ""},
		{"Iranian Rial", "IRR", "﷼", 2, false, ""},
		{"Iraqi Dinar", "IQD", "ع.د", 3, false, ""},
		{"Israeli Shekel", "ILS", "₪", 2, false, ""},
		{"Jamaican Dollar", "JMD", "J$", 2, false, ""},
		{"Jordanian Dinar", "JOD", "د.ا", 3, false, ""},
		{"Kazakhstani Tenge", "KZT", "₸", 2, false, ""},
		{"Kenyan Shilling", "KES", "KSh", 2, false, ""},
		{"Kuwaiti Dinar", "KWD", "د.ك", 3, false, ""},
		{"Kyrgyzstani Som", "KGS", "сом", 2, false, ""},
		{"Lao Kip", "LAK", "₭", 2, false, ""},
		{"Lebanese Pound", "LBP", "ل.ل", 2, false, ""},
		{"Lesotho Loti", "LSL", "L", 2, false, ""},
		{"Liberian Dollar", "LRD", "$", 2, false, ""},
		{"Libyan Dinar", "LYD", "ل.د", 3, false, ""},
		{"Macanese Pataca", "MOP", "MOP$", 2, false, ""},
		{"Malagasy Ariary", "MGA", "Ar", 2, false, ""},
		{"Malawian Kwacha", "MWK", "MK", 2, false, ""},
		{"Malaysian Ringgit", "MYR", "RM", 2, false, ""},
		{"Maldivian Rufiyaa", "MVR", "Rf", 2, false, ""},
		{"Mauritanian Ouguiya", "MRU", "UM", 2, false, ""},
		{"Mauritian Rupee", "MUR", "₨", 2, false, ""},
		{"Moldovan Leu", "MDL", "L", 2, false, ""},
		{"Mongolian Tugrik", "MNT", "₮", 2, false, ""},
		{"Moroccan Dirham", "MAD", "د.م.", 2, false, ""},
		{"Mozambican Metical", "MZN", "MT", 2, false, ""},
		{"Myanmar Kyat", "MMK", "K", 2, false, ""},
		{"Namibian Dollar", "NAD", "$", 2, false, ""},
		{"Nepalese Rupee", "NPR", "₨", 2, false, ""},
		{"Nicaraguan Cordoba", "NIO", "C$", 2, false, ""},
		{"North Korean Won", "KPW", "₩", 2, false, ""},
		{"Norwegian Krone", "NOK", "kr", 2, false, ""},
		{"Omani Rial", "OMR", "﷼", 3, false, ""},
		{"Pakistani Rupee", "PKR", "₨", 2, false, ""},
		{"Panamanian Balboa", "PAB", "B/.", 2, false, ""},
		{"Papua New Guinean Kina", "PGK", "K", 2, false, ""},
		{"Paraguayan Guarani", "PYG", "₲", 0, false, ""},
		{"Qatari Riyal", "QAR", "﷼", 2, false, ""},
		{"Romanian Leu", "RON", "lei", 2, false, ""},
		{"Rwandan Franc", "RWF", "RF", 0, false, ""},
		{"Saudi Riyal", "SAR", "﷼", 2, false, ""},
		{"Serbian Dinar", "RSD", "дин.", 2, false, ""},
		{"Seychellois Rupee", "SCR", "₨", 2, false, ""},
		{"Sierra Leonean Leone", "SLL", "Le", 2, false, ""},
		{"Singapore Dollar", "SGD", "S$", 2, false, ""},
		{"Solomon Islands Dollar", "SBD", "$", 2, false, ""},
		{"Somali Shilling", "SOS", "Sh", 2, false, ""},
		{"South African Rand", "ZAR", "R", 2, false, ""},
		{"South Sudanese Pound", "SSP", "£", 2, false, ""},
		{"Sri Lankan Rupee", "LKR", "₨", 2, false, ""},
		{"Sudanese Pound", "SDG", "ج.س.", 2, false, ""},
		{"Surinamese Dollar", "SRD", "$", 2, false, ""},
		{"Swazi Lilangeni", "SZL", "E", 2, false, ""},
		{"Swedish Krona", "SEK", "kr", 2, false, ""},
		{"Syrian Pound", "SYP", "£", 2, false, ""},
		{"Taiwan Dollar", "TWD", "NT$", 2, false, ""},
		{"Tajikistani Somoni", "TJS", "SM", 2, false, ""},
		{"Tanzanian Shilling", "TZS", "TSh", 2, false, ""},
		{"Thai Baht", "THB", "฿", 2, false, ""},
		{"Tongan Paanga", "TOP", "T$", 2, false, ""},
		{"Trinidad Dollar", "TTD", "TT$", 2, false, ""},
		{"Tunisian Dinar", "TND", "د.ت", 3, false, ""},
		{"Turkmen Manat", "TMT", "T", 2, false, ""},
		{"Ugandan Shilling", "UGX", "USh", 0, false, ""},
		{"Ukrainian Hryvnia", "UAH", "₴", 2, false, ""},
		{"UAE Dirham", "AED", "د.إ", 2, false, ""},
		{"Uruguayan Peso", "UYU", "$U", 2, false, ""},
		{"Uzbekistani Som", "UZS", "сўм", 2, false, ""},
		{"Vanuatu Vatu", "VUV", "VT", 0, false, ""},
		{"Venezuelan Bolivar", "VES", "Bs.S", 2, false, ""},
		{"West African CFA", "XOF", "CFA", 0, false, ""},
		{"Central African CFA", "XAF", "FCFA", 0, false, ""},
		{"CFP Franc", "XPF", "₣", 0, false, ""},
		{"Samoan Tala", "WST", "T", 2, false, ""},
		{"Yemeni Rial", "YER", "﷼", 2, false, ""},
		{"Zambian Kwacha", "ZMW", "ZK", 2, false, ""},
	}

	for _, f := range fiats {
		var existing models.Currency
		if err := facades.Orm().Query().Where("code", f.Code).First(&existing); err == nil && existing.ID != uuid.Nil {
			slog.Info("currency already exists, skipping", "code", f.Code)
			continue
		}
		price := 0.0
		if f.Code == "USD" {
			price = 1.0
		}
		cur := models.Currency{
			ID:           uuid.New(),
			Name:         f.Name,
			Code:         f.Code,
			Symbol:       f.Symbol,
			Type:         models.CurrencyTypeFiat,
			Subunits:     f.Subunits,
			CurrentPrice: price,
			Active:       activeFiatCodes[f.Code],
		}
		if err := facades.Orm().Query().Create(&cur); err != nil {
			return err
		}
		slog.Info("created fiat currency", "code", f.Code)
	}
	return nil
}
