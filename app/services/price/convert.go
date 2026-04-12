package price

import (
	"context"
	"fmt"
)

func (s *Service) Convert(ctx context.Context, from, to string, amount float64) (float64, error) {
	if from == to {
		return amount, nil
	}
	fromPrice, err := s.GetPrice(ctx, from)
	if err != nil {
		return 0, fmt.Errorf("price for %s: %w", from, err)
	}
	toPrice, err := s.GetPrice(ctx, to)
	if err != nil {
		return 0, fmt.Errorf("price for %s: %w", to, err)
	}
	if toPrice == 0 {
		return 0, fmt.Errorf("zero price for %s", to)
	}
	return amount * fromPrice / toPrice, nil
}

func (s *Service) ConvertToUSD(ctx context.Context, code string, amount float64) (float64, error) {
	return s.Convert(ctx, code, "USD", amount)
}

func (s *Service) ConvertFromUSD(ctx context.Context, code string, amount float64) (float64, error) {
	return s.Convert(ctx, "USD", code, amount)
}

func (s *Service) ConvertCryptoToFiat(ctx context.Context, cryptoCode, fiatCode string, amount float64) (float64, error) {
	return s.Convert(ctx, cryptoCode, fiatCode, amount)
}
