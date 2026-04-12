package price

type PriceProvider interface {
	Name() string
	FetchCryptoPrices(codes []string) (map[string]float64, error)
	FetchFiatRates(codes []string) (map[string]float64, error)
}
