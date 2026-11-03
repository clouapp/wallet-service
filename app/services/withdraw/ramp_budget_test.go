package withdraw

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/tests/memcache"
	"github.com/shopspring/decimal"
	"sync"
	"testing"
	"time"
)

func TestRampFundingBudget_RetryBurstCountsOneReservation(t *testing.T) {
	cache := memcache.New()
	quote := &fixedQuote{usd: decimal.NewFromInt(75)}
	s := &Service{cache: cache, usdQuote: quote}
	token := &models.AccessToken{ID: uuid.New(), SpendingLimit: `{"daily_usd":"100"}`}
	order := uuid.NewString()
	start := make(chan struct{})
	errs := make(chan error, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- s.AuthorizeRampFunding(context.Background(), token, order, "USDC", "75")
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if quote.calls != 1 {
		t.Fatalf("retried reservation quoted %d times", quote.calls)
	}
	if got := cache.GetInt(spendingLimitKey(token.ID, time.Now().UTC())); got != 7500 {
		t.Fatalf("spent cents=%d", got)
	}
	if err := s.AuthorizeRampFunding(context.Background(), token, uuid.NewString(), "USDC", "75"); !errors.Is(err, ErrSpendingLimitExceeded) {
		t.Fatalf("next spend: %v", err)
	}
	if err := s.AuthorizeRampFunding(context.Background(), token, order, "USDC", "76"); !errors.Is(err, ErrSpendingLimitInvalid) {
		t.Fatalf("changed reservation: %v", err)
	}
}
