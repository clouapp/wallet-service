package ramps_test

import (
	"context"
	"errors"
	"testing"

	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/tests/support"
)

// quotedRate is what an order carries from the quote it redeemed.
type quotedRate struct{ rate, receive string }

// An order answers the rate the user agreed to next to what settled, read from
// the quote it redeemed. A quote that cannot be read leaves both blank rather
// than failing an order that is otherwise a correct answer.
func TestShowAnswersTheRateTheOrderWasQuoted(t *testing.T) {
	cases := map[string]struct {
		quote *models.RampEstimate
		err   error
		want  quotedRate
	}{
		"quote on file":      {quote: storedEstimate(models.RailPixPayin), want: quotedRate{"0.185000", "18.222500"}},
		"quote unreadable":   {quote: &models.RampEstimate{RawResponse: "{"}},
		"quote lookup fails": {err: errors.New("pq: connection reset")},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, models.RailPixPayin, models.ProviderBridge)
			tx := storedOrder(models.TransactionTypeOnramp, models.TxStatusCompleted, "trf_1")
			tx.EstimateID = support.Ptr(uint(42))
			h.txRepo.EXPECT().FindByCustomerAndExternalID(userID, orderID).Return(tx, nil).Once()
			h.estimateRepo.ExpectedCalls = nil
			h.estimateRepo.EXPECT().FindByCustomerAndID(userID, uint(42)).Return(c.quote, c.err).Once()

			view, err := h.onramp.Show(context.Background(), userID, orderID)
			require.NoError(t, err)
			require.Equal(t, c.want, quotedRate{view.Rate, view.EstimatedReceive})
		})
	}
}

// A settled off-ramp answers both halves of what the provider kept: the gap
// its quote named, and the exchange fee its receipt charged, in the token.
func TestShowAnswersTheProvidersConversionGapAndItsSettledFee(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	tx := storedOrder(models.TransactionTypeOfframp, models.TxStatusCompleted, "trf_1")
	tx.EstimateID = support.Ptr(uint(42))
	tx.ExchangeFeeAmount = support.Ptr("0.060000000000000000")
	quote := storedOfframpEstimate()
	quote.RawResponse = `{"rail":"pix_payout","source_amount":"10.00","estimated_receive":"51.82",` +
		`"rate":"5.182141","fee_percent":"0.00","fee_amount":"0.000000","fee_currency":"usdc",` +
		`"provider_fee_percent":"0.55","provider_fee_amount":"0.29"}`
	h.txRepo.EXPECT().FindByCustomerAndExternalID(userID, orderID).Return(tx, nil).Once()
	h.estimateRepo.ExpectedCalls = nil
	h.estimateRepo.EXPECT().FindByCustomerAndID(userID, uint(42)).Return(quote, nil).Once()

	view, err := h.offramp.Show(context.Background(), userID, orderID)

	require.NoError(t, err)
	require.Equal(t, "0.55", view.ProviderFeePercent)
	require.Equal(t, "0.29", view.ProviderFeeAmount)
	require.Equal(t, "0.060000000000000000", view.ExchangeFeeAmount)
	require.Equal(t, "usdc", view.ExchangeFeeCurrency)
}

// A USD deposit names its fiat leg in dollars: amount_fiat and the exchange
// fee's unit say usd, and amount_brl, which names reais, is left out.
func TestShowAnswersAUSDDepositInDollars(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderBridge)
	tx := storedOrder(models.TransactionTypeOnramp, models.TxStatusCompleted, "trf_1")
	tx.FiatCurrency = models.FiatCurrencyUSD
	tx.AmountFiat = "150.00"
	tx.ExchangeFeeAmount = support.Ptr("0.75")
	h.txRepo.EXPECT().FindByCustomerAndExternalID(userID, orderID).Return(tx, nil).Once()

	view, err := h.onramp.Show(context.Background(), userID, orderID)

	require.NoError(t, err)
	require.Equal(t, "usd", view.FiatCurrency)
	require.Equal(t, "150.00", view.AmountFiat)
	require.Empty(t, view.AmountBRL)
	require.Equal(t, "usd", view.ExchangeFeeCurrency)
}

// A list loads every quote its page names in one read, once per distinct
// quote, and a failed read costs the rates rather than the page.
func TestListLoadsThePagesQuotesInOneRead(t *testing.T) {
	cases := map[string]struct {
		err  error
		want quotedRate
	}{
		"quotes on file":     {want: quotedRate{"0.185000", "18.222500"}},
		"quote lookup fails": {err: errors.New("pq: connection reset")},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, models.RailPixPayin, models.ProviderBridge)
			quoted := func() models.Transaction {
				tx := *storedOrder(models.TransactionTypeOnramp, models.TxStatusCompleted, "trf_1")
				tx.EstimateID = support.Ptr(uint(42))
				return tx
			}
			unquoted := *storedOrder(models.TransactionTypeOnramp, models.TxStatusCompleted, "trf_2")
			h.txRepo.EXPECT().Paginate(testifymock.MatchedBy(func(f repositories.TransactionListFilters) bool {
				return f.CustomerID == userID && f.Type == models.TransactionTypeOnramp
			})).Return([]models.Transaction{quoted(), quoted(), unquoted}, 3, nil).Once()
			var quotes map[uint]models.RampEstimate
			if c.err == nil {
				quotes = map[uint]models.RampEstimate{42: *storedEstimate(models.RailPixPayin)}
			}
			h.estimateRepo.EXPECT().FindByCustomerAndIDs(userID, []uint{42}).Return(quotes, c.err).Once()

			views, total, err := h.onramp.List(userID, 1, 20)
			require.NoError(t, err)
			require.EqualValues(t, 3, total)
			require.Equal(t, c.want, quotedRate{views[0].Rate, views[0].EstimatedReceive})
			require.Equal(t, c.want, quotedRate{views[1].Rate, views[1].EstimatedReceive})
			require.Equal(t, quotedRate{}, quotedRate{views[2].Rate, views[2].EstimatedReceive})
		})
	}
}
