package repositories_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/pkg/numeric"
	"github.com/macrowallets/waas/tests/mocks"
)

// NumericColumnsTestSuite round-trips every numeric(p,s) column through PostgreSQL
// with the exact decimal types: the digits read back must equal the digits written.
type NumericColumnsTestSuite struct {
	suite.Suite
}

func TestNumericColumnsSuite(t *testing.T) {
	suite.Run(t, new(NumericColumnsTestSuite))
}

func (s *NumericColumnsTestSuite) SetupTest() {
	mocks.TestDB(s.T())
}

func (s *NumericColumnsTestSuite) exact(text string) decimal.Decimal {
	value, err := decimal.NewFromString(text)
	s.Require().NoError(err)
	return value
}

func (s *NumericColumnsTestSuite) columnText(table, column, idColumn string, id any) *string {
	var rows []struct {
		Value *string `gorm:"column:value"`
	}
	s.Require().NoError(facades.Orm().Query().Raw("SELECT "+column+"::text AS value FROM "+table+" WHERE "+idColumn+" = ?", id).Scan(&rows))
	s.Require().Len(rows, 1)
	return rows[0].Value
}

func (s *NumericColumnsTestSuite) insertChain(id string, dustUSD numeric.NullDecimal) {
	chain := models.Chain{
		ID: id, Name: id, AdapterType: models.AdapterTypeEVM, NativeSymbol: "ETH", NativeDecimals: 18,
		RpcURL: "encrypted-rpc", RequiredConfirmations: 12, Status: "active", DustThresholdUSD: dustUSD,
	}
	s.Require().NoError(facades.Orm().Query().Create(&chain))
}

func (s *NumericColumnsTestSuite) TestWalletFeeMultiplierAndBalanceUSDRoundTripExactly() {
	repo := repositories.NewWalletRepository()
	wallet := mocks.InsertWallet(s.T(), "eth")

	feeMultiplier := s.exact("1.2345")
	balanceUSD := s.exact("12345678901234567.0123456789")
	s.Require().NoError(repo.UpdateFields(wallet.ID, map[string]interface{}{
		"fee_multiplier": numeric.NewNullDecimal(feeMultiplier),
		"balance_usd":    numeric.NewNullDecimal(balanceUSD),
	}))

	found, err := repo.FindByID(wallet.ID)
	s.Require().NoError(err)
	s.Require().NotNil(found)
	s.True(found.FeeMultiplier.Valid)
	s.True(found.FeeMultiplier.Decimal.Equal(feeMultiplier), "fee_multiplier = %s", found.FeeMultiplier.Decimal)
	s.True(found.BalanceUSD.Decimal.Equal(balanceUSD), "balance_usd = %s", found.BalanceUSD.Decimal)
	s.Equal("12345678901234567.0123456789", *s.columnText("wallets", "balance_usd", "id", wallet.ID))

	raw, err := json.Marshal(found)
	s.Require().NoError(err)
	s.Contains(string(raw), `"fee_multiplier":1.2345,`)
	s.Contains(string(raw), `"balance_usd":12345678901234567.0123456789,`)
}

func (s *NumericColumnsTestSuite) TestWalletDecimalUpdatedThroughUpdateFieldAndClearedToNull() {
	repo := repositories.NewWalletRepository()
	wallet := mocks.InsertWallet(s.T(), "eth")

	s.Require().NoError(repo.UpdateField(wallet.ID, "fee_multiplier", s.exact("0.1")))
	s.Equal("0.1000", *s.columnText("wallets", "fee_multiplier", "id", wallet.ID))

	s.Require().NoError(repo.UpdateField(wallet.ID, "fee_multiplier", numeric.NullDecimal{}))
	s.Nil(s.columnText("wallets", "fee_multiplier", "id", wallet.ID))

	found, err := repo.FindByID(wallet.ID)
	s.Require().NoError(err)
	s.False(found.FeeMultiplier.Valid)
	s.False(found.BalanceUSD.Valid)
	raw, err := json.Marshal(found)
	s.Require().NoError(err)
	s.NotContains(string(raw), "fee_multiplier")
	s.NotContains(string(raw), "balance_usd")
}

func (s *NumericColumnsTestSuite) TestAssetBalancePriceAndValueRoundTripExactly() {
	s.insertChain("eth", numeric.NewNullDecimal(s.exact("1.0000")))
	wallet := mocks.InsertWallet(s.T(), "eth")
	repo := repositories.NewWalletAssetBalanceRepository()

	price := s.exact("0.0000123457")
	value := s.exact("123456789.9999999999")
	rows := []models.WalletAssetBalance{
		{ID: uuid.New(), WalletID: wallet.ID, ChainID: "eth", AssetType: "native", AssetSymbol: "ETH", AssetKey: "ETH", Decimals: 18, AmountRaw: "1", AmountDisplay: "0.000000000000000001", PriceUSD: numeric.NewNullDecimal(price), ValueUSD: numeric.NewNullDecimal(value), LastSyncedAt: time.Now()},
		{ID: uuid.New(), WalletID: wallet.ID, ChainID: "eth", AssetType: "token", AssetSymbol: "USDC", AssetKey: "eth:USDC", Decimals: 6, AmountRaw: "0", AmountDisplay: "0", LastSyncedAt: time.Now()},
	}
	s.Require().NoError(repo.ReplaceForWallet(wallet.ID, "eth", rows))

	listed, err := repo.ListByWallet(wallet.ID)
	s.Require().NoError(err)
	s.Require().Len(listed, 2)
	bySymbol := map[string]models.WalletAssetBalance{}
	for _, row := range listed {
		bySymbol[row.AssetSymbol] = row
	}
	s.True(bySymbol["ETH"].PriceUSD.Decimal.Equal(price), "price_usd = %s", bySymbol["ETH"].PriceUSD.Decimal)
	s.True(bySymbol["ETH"].ValueUSD.Decimal.Equal(value), "value_usd = %s", bySymbol["ETH"].ValueUSD.Decimal)
	s.False(bySymbol["USDC"].PriceUSD.Valid)
	s.False(bySymbol["USDC"].ValueUSD.Valid)
}

func (s *NumericColumnsTestSuite) TestSnapshotBalanceUSDRoundTripsExactly() {
	s.insertChain("eth", numeric.NullDecimal{})
	wallet := mocks.InsertWallet(s.T(), "eth")
	repo := repositories.NewWalletBalanceSnapshotRepository()

	balanceUSD := s.exact("0.0000000001")
	s.Require().NoError(repo.Create(&models.WalletBalanceSnapshot{
		ID: uuid.New(), WalletID: wallet.ID, ChainID: "eth", BalanceAsset: "ETH",
		BalanceRaw: "1", BalanceDisplay: "0.000000000000000001", BalanceUSD: numeric.NewNullDecimal(balanceUSD), CapturedAt: time.Now(),
	}))

	recent, err := repo.ListRecent(wallet.ID, "eth", 1)
	s.Require().NoError(err)
	s.Require().Len(recent, 1)
	s.True(recent[0].BalanceUSD.Decimal.Equal(balanceUSD), "balance_usd = %s", recent[0].BalanceUSD.Decimal)
}

func (s *NumericColumnsTestSuite) TestChainDustThresholdUSDRoundTripsAndKeepsNull() {
	s.insertChain("base", numeric.NewNullDecimal(s.exact("0.10")))
	s.insertChain("btc", numeric.NullDecimal{})
	repo := repositories.NewChainRepository()

	base, err := repo.FindByID("base")
	s.Require().NoError(err)
	s.True(base.DustThresholdUSD.Valid)
	s.True(base.DustThresholdUSD.Decimal.Equal(s.exact("0.1")))
	s.Equal("0.1000", *s.columnText("chains", "dust_threshold_usd", "id", "base"))

	btc, err := repo.FindByID("btc")
	s.Require().NoError(err)
	s.False(btc.DustThresholdUSD.Valid)
}

func (s *NumericColumnsTestSuite) TestCurrencyPricesRoundTripAndUpdateExactly() {
	repo := repositories.NewCurrencyRepository()
	s.Require().NoError(repo.Create(&models.Currency{
		Name: "Bitcoin", Code: "BTC", Symbol: "₿", Type: models.CurrencyTypeCrypto, Subunits: 8,
		CurrentPrice: numeric.NewDecimal(s.exact("65000.1234567891")), Active: true,
	}))

	created, err := repo.FindByCode("BTC")
	s.Require().NoError(err)
	s.True(created.CurrentPrice.Equal(s.exact("65000.1234567891")), "current_price = %s", created.CurrentPrice)
	s.False(created.LastPrice.Valid)

	s.Require().NoError(repo.UpdatePrice("BTC", s.exact("0.19607843137254902"), created.CurrentPrice.Decimal))
	updated, err := repo.FindByCode("BTC")
	s.Require().NoError(err)
	s.True(updated.CurrentPrice.Equal(s.exact("0.1960784314")), "current_price = %s", updated.CurrentPrice)
	s.True(updated.LastPrice.Valid)
	s.True(updated.LastPrice.Decimal.Equal(s.exact("65000.1234567891")), "last_price = %s", updated.LastPrice.Decimal)

	raw, err := json.Marshal(updated)
	s.Require().NoError(err)
	s.Contains(string(raw), `"current_price":0.1960784314,`)
	s.Contains(string(raw), `"last_price":65000.1234567891,`)
}

func (s *NumericColumnsTestSuite) TestCurrencyZeroPriceLeavesTheColumnDefault() {
	repo := repositories.NewCurrencyRepository()
	s.Require().NoError(repo.Create(&models.Currency{Name: "Euro", Code: "EUR", Symbol: "€", Type: models.CurrencyTypeFiat, Subunits: 2}))

	created, err := repo.FindByCode("EUR")
	s.Require().NoError(err)
	s.True(created.CurrentPrice.Equal(decimal.NewFromInt(1)), "current_price = %s", created.CurrentPrice)
}

func (s *NumericColumnsTestSuite) TestCurrencyUpdatePriceRejectsUnstorablePrices() {
	repo := repositories.NewCurrencyRepository()
	s.Require().NoError(repo.Create(&models.Currency{Name: "Bitcoin", Code: "BTC", Symbol: "₿", Type: models.CurrencyTypeCrypto, Subunits: 8, CurrentPrice: numeric.NewDecimal(s.exact("65000"))}))

	cases := map[string]error{"0": numeric.ErrNotPositive, "-1": numeric.ErrNotPositive, "0.00000000001": numeric.ErrNotPositive, "1e18": numeric.ErrOutOfRange}
	for text, want := range cases {
		err := repo.UpdatePrice("BTC", s.exact(text), s.exact("65000"))
		s.True(errors.Is(err, want), "price %s: err = %v, want %v", text, err, want)
	}
	s.Error(repo.UpdatePrice(" ", s.exact("1"), s.exact("1")))

	unchanged, err := repo.FindByCode("BTC")
	s.Require().NoError(err)
	s.True(unchanged.CurrentPrice.Equal(s.exact("65000")), "current_price = %s", unchanged.CurrentPrice)
}
