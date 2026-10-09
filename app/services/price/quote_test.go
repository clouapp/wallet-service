package price

import (
	"context"
	"errors"
	"testing"

	"github.com/macrowallets/waas/app/models"
)

func TestQuote_ReturnsTheAmountResultAndRate(t *testing.T) {
	quote, err := newTestService(t).Quote(context.Background(), QuoteInput{From: "BTC", To: "USD", Amount: "0.5"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if quote.From != "BTC" || quote.To != "USD" {
		t.Fatalf("codes = %s/%s", quote.From, quote.To)
	}
	requireDecimal(t, quote.Amount, "0.5")
	requireDecimal(t, quote.Result, "32500")
	requireDecimal(t, quote.Rate, "65000")
}

func TestQuote_TheRateKeepsTheConversionScale(t *testing.T) {
	quote, err := newTestService(t).Quote(context.Background(), QuoteInput{From: "BTC", To: "BRL", Amount: "3"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	requireDecimal(t, quote.Result, "994897.959183673469387755")
	requireDecimal(t, quote.Rate, "331632.653061224489795918")
}

func TestQuote_RefusesWhatTheHandlerRefusedBeforeConverting(t *testing.T) {
	cases := []struct {
		name  string
		input QuoteInput
		want  error
	}{
		{"missing from", QuoteInput{To: "USD", Amount: "1"}, ErrQuoteFieldsRequired},
		{"missing to", QuoteInput{From: "BTC", Amount: "1"}, ErrQuoteFieldsRequired},
		{"missing amount", QuoteInput{From: "BTC", To: "USD"}, ErrQuoteAmountInvalid},
		{"not a number", QuoteInput{From: "BTC", To: "USD", Amount: "abc"}, ErrQuoteAmountInvalid},
		{"zero", QuoteInput{From: "BTC", To: "USD", Amount: "0"}, ErrQuoteAmountInvalid},
		{"negative", QuoteInput{From: "BTC", To: "USD", Amount: "-1"}, ErrQuoteAmountInvalid},
		{"codes before the amount", QuoteInput{Amount: "abc"}, ErrQuoteFieldsRequired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newTestService(t).Quote(context.Background(), tc.input)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestQuote_PassesTheConversionFailureThrough(t *testing.T) {
	_, err := newTestService(t).Quote(context.Background(), QuoteInput{From: "UNKNOWN", To: "USD", Amount: "1"})
	if !errors.Is(err, ErrCurrencyNotFound) {
		t.Fatalf("error = %v, want currency not found", err)
	}
}

func TestQuote_MarksAStoreFailureAsQuoteFailed(t *testing.T) {
	storeErr := errors.New("pq: connection refused")
	svc := NewService(Deps{Currencies: &failingCurrencyRepo{err: storeErr}})

	_, err := svc.Quote(context.Background(), QuoteInput{From: "BTC", To: "USD", Amount: "1"})

	if !errors.Is(err, ErrQuoteFailed) || !errors.Is(err, storeErr) {
		t.Fatalf("error = %v, want quote failed wrapping the store error", err)
	}
}

type failingCurrencyRepo struct {
	mockCurrencyRepo
	err error
}

func (f *failingCurrencyRepo) FindByCode(context.Context, string) (*models.Currency, error) {
	return nil, f.err
}
