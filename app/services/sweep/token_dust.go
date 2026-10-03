package sweep

import (
	"context"
	"log/slog"
	"math/big"

	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/config"
	"github.com/macrowallets/waas/pkg/types"
)

// TokenPricer quotes a token's USD price from a provider quote; a price nobody
// quoted (the currencies table's placeholder) must be an error, never a guess.
type TokenPricer interface {
	QuotedUSDPrice(ctx context.Context, code string) (decimal.Decimal, error)
}

// childDustThreshold is the smallest balance of asset a child must hold to be
// swept, in base units; nil means no filtering. The native coin uses the
// adapter's threshold (chains.dust_threshold_native_raw). Tokens use
// chains.dust_threshold_usd, or the chain's *_DUST_THRESHOLD_USD default when
// the column is NULL, converted at the token's quoted USD price.
func (s *service) childDustThreshold(ctx context.Context, adapter types.Chain, chainEntity *models.Chain, asset string) *big.Int {
	native := adapter.DustThreshold(asset)
	if types.SameAssetSymbol(asset, adapter.NativeAsset()) || (native != nil && native.Sign() > 0) {
		return native
	}
	return s.tokenDustThreshold(ctx, chainEntity, asset)
}

func (s *service) tokenDustThreshold(ctx context.Context, chainEntity *models.Chain, asset string) *big.Int {
	if s.tokenPricer == nil || chainEntity == nil {
		return nil
	}
	dustUSD := tokenDustUSD(chainEntity)
	if !dustUSD.IsPositive() {
		return nil
	}
	token, err := s.registry.FindToken(chainEntity.ID, asset)
	if err != nil || token == nil {
		return nil
	}
	price, err := s.tokenPricer.QuotedUSDPrice(ctx, token.Symbol)
	if err != nil || !price.IsPositive() {
		slog.Warn("token dust threshold skipped: no quoted price", "chain", chainEntity.ID, "asset", token.Symbol, "error", err)
		return nil
	}
	return dustBaseUnits(dustUSD, price, token.Decimals)
}

// tokenDustUSD is the chain's USD dust threshold: the chains column when set
// (0 disables filtering), else the configured default for the chain.
func tokenDustUSD(chainEntity *models.Chain) decimal.Decimal {
	if chainEntity.DustThresholdUSD.Valid {
		return chainEntity.DustThresholdUSD.Decimal
	}
	defaults, ok := config.SweepDefaults()[chainEntity.ID]
	if !ok {
		return decimal.Zero
	}
	return defaults.DustUSD
}

// dustBaseUnits is ceil(dustUSD ÷ priceUSD × 10^decimals): the fewest base
// units worth at least dustUSD.
func dustBaseUnits(dustUSD, priceUSD decimal.Decimal, decimals uint8) *big.Int {
	if !dustUSD.IsPositive() || !priceUSD.IsPositive() {
		return nil
	}
	tokens := dustUSD.Shift(int32(decimals)).Div(priceUSD)
	return tokens.Ceil().BigInt()
}
