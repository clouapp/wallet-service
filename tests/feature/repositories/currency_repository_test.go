package repositories_test

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/pkg/numeric"
	"github.com/macrowallets/waas/tests/mocks"
)

type CurrencyRepositoryTestSuite struct {
	suite.Suite
	repo *repositories.CurrencyRepository
}

func TestCurrencyRepositorySuite(t *testing.T) {
	suite.Run(t, new(CurrencyRepositoryTestSuite))
}

func (s *CurrencyRepositoryTestSuite) SetupTest() {
	mocks.TestDB(s.T())
	s.repo = repositories.NewCurrencyRepository(nil)
}

func (s *CurrencyRepositoryTestSuite) currency(code, kind string, active bool) *models.Currency {
	return &models.Currency{Name: code, Code: code, Type: kind, Active: active, CurrentPrice: numeric.NewDecimal(decimal.NewFromInt(1))}
}

func (s *CurrencyRepositoryTestSuite) TestFindByCode_NotFound() {
	found, err := s.repo.FindByCode(context.Background(), "NOPE")
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(found)
}

func (s *CurrencyRepositoryTestSuite) TestSetPriceAndFindActive() {
	btc := s.currency("BTC", models.CurrencyTypeCrypto, true)
	usd := s.currency("USD", models.CurrencyTypeFiat, true)
	brl := s.currency("BRL", models.CurrencyTypeFiat, true)
	s.Require().NoError(s.repo.Create(context.Background(), btc))
	s.Require().NoError(s.repo.Create(context.Background(), usd))
	s.Require().NoError(s.repo.Create(context.Background(), brl))

	s.Require().NoError(s.repo.SetPrice(context.Background(), "BTC", decimal.NewFromInt(65000), decimal.NewFromInt(64000)))

	found, err := s.repo.FindByCode(context.Background(), "BTC")
	s.NoError(err)
	s.True(found.CurrentPrice.Equal(decimal.NewFromInt(65000)))
	s.True(found.LastPrice.Valid)
	s.True(found.LastPrice.Decimal.Equal(decimal.NewFromInt(64000)))
	s.Require().NotNil(found.PriceUpdatedAt)

	cryptos, err := s.repo.FindActiveCryptos(context.Background())
	s.NoError(err)
	s.Len(cryptos, 1)

	fiats, err := s.repo.FindActiveFiats(context.Background())
	s.NoError(err)
	s.Len(fiats, 2)

	all, err := s.repo.FindAllActive(context.Background())
	s.NoError(err)
	s.Len(all, 3)
}

func (s *CurrencyRepositoryTestSuite) TestFindStaleSkipsUSDAndFreshRows() {
	old := time.Now().Add(-2 * time.Hour)
	btc := s.currency("BTC", models.CurrencyTypeCrypto, true)
	btc.PriceUpdatedAt = &old
	usd := s.currency("USD", models.CurrencyTypeFiat, true)
	usd.PriceUpdatedAt = &old
	s.Require().NoError(s.repo.Create(context.Background(), btc))
	s.Require().NoError(s.repo.Create(context.Background(), usd))

	stale, err := s.repo.FindStale(context.Background(), models.CurrencyTypeCrypto, time.Minute)
	s.NoError(err)
	s.Len(stale, 1)
	s.Equal("BTC", stale[0].Code)

	fiats, err := s.repo.FindStale(context.Background(), models.CurrencyTypeFiat, time.Minute)
	s.NoError(err)
	s.Empty(fiats)
}
