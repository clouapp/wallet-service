package price

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/pkg/numeric"
)

// ConversionScale is the number of decimal places a conversion result keeps
// (rounded half away from zero).
const ConversionScale int32 = 18

func (s *Service) Convert(ctx context.Context, from, to string, amount decimal.Decimal) (decimal.Decimal, error) {
	if strings.TrimSpace(from) == "" || strings.TrimSpace(to) == "" {
		return decimal.Decimal{}, fmt.Errorf("both currency codes are required to convert")
	}
	if amount.IsNegative() {
		return decimal.Decimal{}, fmt.Errorf("amount %s: %w", amount.String(), numeric.ErrNegative)
	}
	if from == to {
		return amount, nil
	}
	fromPrice, err := s.GetPrice(ctx, from)
	if err != nil {
		return decimal.Decimal{}, fmt.Errorf("price for %s: %w", from, err)
	}
	toPrice, err := s.GetPrice(ctx, to)
	if err != nil {
		return decimal.Decimal{}, fmt.Errorf("price for %s: %w", to, err)
	}
	if !toPrice.IsPositive() {
		return decimal.Decimal{}, fmt.Errorf("zero price for %s", to)
	}
	return amount.Mul(fromPrice).DivRound(toPrice, ConversionScale), nil
}

func (s *Service) ConvertToUSD(ctx context.Context, code string, amount decimal.Decimal) (decimal.Decimal, error) {
	return s.Convert(ctx, code, usdCode, amount)
}

func (s *Service) ConvertFromUSD(ctx context.Context, code string, amount decimal.Decimal) (decimal.Decimal, error) {
	return s.Convert(ctx, usdCode, code, amount)
}

func (s *Service) ConvertCryptoToFiat(ctx context.Context, cryptoCode, fiatCode string, amount decimal.Decimal) (decimal.Decimal, error) {
	return s.Convert(ctx, cryptoCode, fiatCode, amount)
}
