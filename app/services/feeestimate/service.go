// Package feeestimate answers "what fee would this withdrawal pay?" with the
// withdrawal's own planner and adapters (sweep.FeeQuoter), validating inputs the
// way POST /withdrawals does and caching answers briefly.
package feeestimate

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	contractscache "github.com/goravel/framework/contracts/cache"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/sweep"
	"github.com/macrowallets/waas/app/services/withdraw"
	"github.com/macrowallets/waas/pkg/amount"
	"github.com/macrowallets/waas/pkg/types"
)

const (
	// DefaultCacheTTL keeps an estimate long enough to absorb a form's keystrokes
	// while gas prices and fee rates stay current.
	DefaultCacheTTL = 15 * time.Second
	cacheKeyPrefix  = "vault:fee-estimate:v2"

	recipientProvided = "provided"
	recipientProbe    = "probe"

	feeRateSourceEstimator = "estimator"
	feeRateSourceFlat      = "flat_fallback"
	evmTxTypeLegacy        = "legacy"

	milliSatDecimals = 3
	gweiDecimals     = 9
)

// referenceBaseUnits is quoted when the caller gives no amount: the smallest transfer.
var referenceBaseUnits = big.NewInt(1)

// ChainCatalog reads chain rows (decimals, adapter type).
type ChainCatalog interface {
	FindByID(id string) (*models.Chain, error)
}

// AdapterRegistry resolves a chain's adapter and its seeded tokens.
type AdapterRegistry interface {
	Chain(id string) (types.Chain, error)
	TokensForChain(chainID string) []types.Token
}

// minimumTransferAmounter is implemented by chains with a minimum transfer (Bitcoin dust).
type minimumTransferAmounter interface {
	MinimumTransferAmount() *big.Int
}

// Request asks for the fee of withdrawing Amount (human decimal, optional) of Asset
// (optional, native by default) from Wallet to To (optional).
type Request struct {
	Wallet          *models.Wallet
	Asset           string
	Amount          string
	To              string
	CallerAccountID uuid.UUID
}

// Service builds estimates; construct it with NewService.
type Service struct {
	quoter   sweep.FeeQuoter
	registry AdapterRegistry
	chains   ChainCatalog
	cache    contractscache.Driver
	cacheTTL time.Duration
	now      func() time.Time
}

// Deps is everything the fee estimator needs. A nil field means that
// dependency is absent. Cache may be nil; CacheTTL ≤ 0 disables caching.
// A nil Now uses time.Now.
type Deps struct {
	Quoter   sweep.FeeQuoter
	Registry AdapterRegistry
	Chains   ChainCatalog
	Cache    contractscache.Driver
	CacheTTL time.Duration
	Now      func() time.Time
}

// NewService wires an estimator from Deps.
func NewService(deps Deps) (*Service, error) {
	if deps.Quoter == nil || deps.Registry == nil || deps.Chains == nil {
		return nil, fmt.Errorf("feeestimate: quoter, registry and chain catalog are required")
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	return &Service{quoter: deps.Quoter, registry: deps.Registry, chains: deps.Chains, cache: deps.Cache, cacheTTL: deps.CacheTTL, now: now}, nil
}

// resolvedRequest is a validated Request in base units.
type resolvedRequest struct {
	wallet      *models.Wallet
	chain       *models.Chain
	asset       withdraw.ResolvedAsset
	baseUnits   *big.Int
	isReference bool
	to          string
}

// Estimate validates req, then returns a cached or fresh estimate. Every failure is an *Error.
func (s *Service) Estimate(ctx context.Context, req Request) (*Estimate, error) {
	resolved, err := s.resolve(req)
	if err != nil {
		return nil, err
	}
	key := cacheKey(resolved)
	if cached, ok := s.cached(key); ok {
		return cached, nil
	}
	quote, err := s.quoter.QuoteWithdrawalFee(ctx, sweep.FeeQuoteRequest{
		WalletID:        resolved.wallet.ID,
		Asset:           resolved.asset.WalletAsset,
		Amount:          resolved.baseUnits,
		ToAddress:       resolved.to,
		CallerAccountID: req.CallerAccountID,
	})
	if err != nil {
		return nil, classifyQuoteError(err)
	}
	estimatedAt := s.now().UTC()
	estimate := buildEstimate(resolved, quote, estimatedAt, estimatedAt.Add(max(s.cacheTTL, 0)))
	s.store(key, estimate)
	return estimate, nil
}

func (s *Service) resolve(req Request) (*resolvedRequest, error) {
	if req.Wallet == nil {
		return nil, newError(KindInvalidInput, CodeInvalidAmount, "wallet is required", nil)
	}
	chainRow, err := s.chains.FindByID(req.Wallet.Chain)
	if err != nil || chainRow == nil {
		return nil, newError(KindUnprocessable, CodeUnsupportedChain, fmt.Sprintf("chain %s is not configured", req.Wallet.Chain), err)
	}
	adapter, err := s.registry.Chain(req.Wallet.Chain)
	if err != nil {
		return nil, newError(KindUnprocessable, CodeUnsupportedChain, fmt.Sprintf("chain %s has no adapter", req.Wallet.Chain), err)
	}

	to := strings.TrimSpace(req.To)
	if to != "" && !adapter.ValidateAddress(to) {
		return nil, newError(KindUnprocessable, CodeInvalidAddress, fmt.Sprintf("to is not a valid %s address", req.Wallet.Chain), nil)
	}

	asset, err := withdraw.ResolveAsset(req.Wallet.Chain, adapter.NativeAsset(), chainRow.NativeDecimals, req.Asset, s.registry.TokensForChain(req.Wallet.Chain))
	if err != nil {
		return nil, classifyAssetError(err)
	}

	resolved := &resolvedRequest{wallet: req.Wallet, chain: chainRow, asset: asset, to: to}
	minimum := minimumTransfer(adapter, asset)
	if strings.TrimSpace(req.Amount) == "" {
		resolved.baseUnits, resolved.isReference = minimum, true
		return resolved, nil
	}
	baseUnits, err := withdraw.ParseHumanAmount(req.Amount, asset.Decimals)
	if err != nil {
		return nil, newError(KindInvalidInput, CodeInvalidAmount, err.Error(), err)
	}
	if baseUnits.Cmp(minimum) < 0 {
		return nil, newError(KindUnprocessable, CodeAmountBelowMinimum,
			fmt.Sprintf("amount is below the minimum transfer of %s %s", amount.FormatBaseUnits(minimum, asset.Decimals), asset.WalletAsset), nil)
	}
	resolved.baseUnits = baseUnits
	return resolved, nil
}

// minimumTransfer is the smallest amount the adapter builds a transfer for.
func minimumTransfer(adapter types.Chain, asset withdraw.ResolvedAsset) *big.Int {
	if asset.Token == nil {
		if limited, ok := adapter.(minimumTransferAmounter); ok {
			if minimum := limited.MinimumTransferAmount(); minimum != nil && minimum.Sign() > 0 {
				return new(big.Int).Set(minimum)
			}
		}
	}
	return new(big.Int).Set(referenceBaseUnits)
}

func cacheKey(req *resolvedRequest) string {
	to := req.to
	if to == "" {
		to = recipientProbe
	}
	return fmt.Sprintf("%s:%s:%s:%s:%s:%s", cacheKeyPrefix, req.wallet.ID, chain.FeePolicyFingerprint(req.wallet),
		strings.ToUpper(req.asset.WalletAsset), req.baseUnits, to)
}

// cached returns a stored estimate. An unreadable entry is logged and treated as a miss.
func (s *Service) cached(key string) (*Estimate, bool) {
	if s.cache == nil || s.cacheTTL <= 0 {
		return nil, false
	}
	raw := s.cache.GetString(key, "")
	if raw == "" {
		return nil, false
	}
	var estimate Estimate
	if err := json.Unmarshal([]byte(raw), &estimate); err != nil {
		slog.Warn("fee estimate cache entry unreadable", "error", err)
		return nil, false
	}
	estimate.Cached = true
	return &estimate, true
}

func (s *Service) store(key string, estimate *Estimate) {
	if s.cache == nil || s.cacheTTL <= 0 {
		return
	}
	raw, err := json.Marshal(estimate)
	if err != nil {
		slog.Warn("fee estimate cache encode failed", "error", err)
		return
	}
	if err := s.cache.Put(key, string(raw), s.cacheTTL); err != nil {
		slog.Warn("fee estimate cache write failed", "error", err)
	}
}
