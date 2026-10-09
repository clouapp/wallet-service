package price

import (
	"context"
	"errors"
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/pkg/numeric"
)

// Refusals Quote answers before it converts anything.
var (
	ErrQuoteFieldsRequired = errors.New("from, to, and amount are required")
	ErrQuoteAmountInvalid  = errors.New("amount must be a positive number")
	// ErrQuoteFailed wraps a conversion failure that is not a refusal the caller
	// can fix or a provider fault: the price store failed. The cause stays on
	// the error chain.
	ErrQuoteFailed = errors.New("currency conversion failed")
)

// QuoteInput is a conversion request as a client wrote it: the codes as typed
// and the amount as text.
type QuoteInput struct {
	From   string
	To     string
	Amount string
}

// Quote is a conversion with the rate it implies.
type Quote struct {
	From   string
	To     string
	Amount decimal.Decimal
	Result decimal.Decimal
	Rate   decimal.Decimal
}

// Quote converts a positive amount from one currency to another. Both codes are
// required and the amount must parse to a positive number; the codes are
// checked first. The rate is result per unit, at the conversion scale.
func (s *Service) Quote(ctx context.Context, in QuoteInput) (Quote, error) {
	if in.From == "" || in.To == "" {
		return Quote{}, ErrQuoteFieldsRequired
	}
	amount, err := numeric.Parse("amount", in.Amount)
	if err != nil || !amount.IsPositive() {
		return Quote{}, ErrQuoteAmountInvalid
	}

	result, err := s.Convert(ctx, in.From, in.To, amount)
	if err != nil {
		return Quote{}, classifyConvertError(err)
	}
	return Quote{
		From:   in.From,
		To:     in.To,
		Amount: amount,
		Result: result,
		Rate:   result.DivRound(amount, ConversionScale),
	}, nil
}

// classifyConvertError keeps the sentinels Convert wraps for a caller mistake or
// a provider fault, and marks any other failure as ErrQuoteFailed.
func classifyConvertError(err error) error {
	for _, known := range []error{ErrPriceNotQuoted, ErrZeroPrice, ErrCurrencyNotFound, ErrCurrencyCodeRequired, ErrCurrencyCodesRequired} {
		if errors.Is(err, known) {
			return err
		}
	}
	return fmt.Errorf("%w: %w", ErrQuoteFailed, err)
}
