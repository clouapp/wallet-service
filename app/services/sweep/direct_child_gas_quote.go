package sweep

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

type directGasContext struct {
	wallet  *models.Wallet
	child   models.Address
	chain   *models.Chain
	adapter types.Chain
	token   *types.Token
	amount  *big.Int
}

func gasPositive(text string) (*big.Int, error) {
	v, ok := new(big.Int).SetString(text, 10)
	if !ok || v.Sign() <= 0 || v.String() != text {
		return nil, ErrGasPreparationBinding
	}
	return v, nil
}

func gasNonnegative(text string) (*big.Int, error) {
	v, ok := new(big.Int).SetString(text, 10)
	if !ok || v.Sign() < 0 || v.String() != text {
		return nil, ErrGasPreparationBinding
	}
	return v, nil
}

func gasBindingHash(b DirectChildGasBinding) string {
	b.SourceAddress, b.Recipient, b.TokenContract = strings.ToLower(b.SourceAddress), strings.ToLower(b.Recipient), strings.ToLower(b.TokenContract)
	b.Asset = strings.ToUpper(b.Asset)
	raw, _ := json.Marshal(b)
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

func (s *service) directGasContext(ctx context.Context, b DirectChildGasBinding) (*directGasContext, error) {
	if b.AccountID == uuid.Nil || b.WalletID == uuid.Nil || b.SourceAddressID == uuid.Nil || b.NetworkID <= 0 || b.SourceDerivationIndex <= 0 {
		return nil, ErrGasPreparationBinding
	}
	amount, err := gasPositive(b.AmountRaw)
	if err != nil {
		return nil, err
	}
	wallet, err := s.walletRepo.FindByID(ctx, b.WalletID)
	if err != nil {
		return nil, err
	}
	if wallet == nil || wallet.ID != b.WalletID || wallet.AccountID == nil || *wallet.AccountID != b.AccountID || wallet.Status != "active" || wallet.FrozenUntil != nil || wallet.DepositAddress == nil || wallet.Chain != b.Chain {
		return nil, ErrGasPreparationBinding
	}
	base := wallet.DepositAddress
	if base.WalletID != wallet.ID || base.Chain != b.Chain || !base.IsActive || base.DerivationIndex != 0 {
		return nil, ErrGasPreparationBinding
	}
	entity, err := s.loadChain(ctx, b.Chain)
	if err != nil {
		return nil, err
	}
	if entity == nil || entity.Status != "active" || entity.AdapterType != models.AdapterTypeEVM || entity.NetworkID == nil || *entity.NetworkID != b.NetworkID {
		return nil, ErrGasPreparationBinding
	}
	adapter, err := s.registry.ChainForWallet(wallet)
	if err != nil {
		return nil, err
	}
	if !evmTokenGasApplies(adapter, b.Asset) || !adapter.ValidateAddress(b.Recipient) || !adapter.ValidateAddress(wallet.DepositAddress.Address) {
		return nil, ErrGasPreparationBinding
	}
	token, err := s.registry.FindToken(b.Chain, b.Asset)
	if err != nil {
		return nil, err
	}
	if token == nil || !strings.EqualFold(token.Contract, b.TokenContract) || token.Decimals != b.TokenDecimals {
		return nil, ErrGasPreparationBinding
	}
	addresses, err := s.addressRepo.FindByWalletID(ctx, b.WalletID)
	if err != nil {
		return nil, err
	}
	for _, child := range addresses {
		if child.ID != b.SourceAddressID {
			continue
		}
		if child.WalletID != wallet.ID || child.ID == wallet.DepositAddress.ID || child.Chain != b.Chain || !child.IsActive || child.DerivationIndex != b.SourceDerivationIndex || child.ExternalUserID != b.SourceExternalUserID || !strings.EqualFold(child.Address, b.SourceAddress) || !adapter.ValidateAddress(child.Address) {
			return nil, ErrGasPreparationBinding
		}
		return &directGasContext{wallet: wallet, child: child, chain: entity, adapter: adapter, token: token, amount: amount}, nil
	}
	return nil, ErrGasPreparationBinding
}

func (g *directGasContext) funderReserve() *big.Int {
	reserve := new(big.Int)
	for _, threshold := range []*big.Int{g.chain.GasReadinessThreshold(), g.adapter.GasReadinessThreshold()} {
		if threshold != nil && threshold.Sign() > 0 && threshold.Cmp(reserve) > 0 {
			reserve.Set(threshold)
		}
	}
	return reserve
}

// QuoteDirectChildGas simulates the actual child's token transfer, never a
// fictitiously funded base. It reads only and does not build or sign transactions.
func (s *service) QuoteDirectChildGas(ctx context.Context, b DirectChildGasBinding) (*DirectChildGasPlan, error) {
	g, err := s.directGasContext(ctx, b)
	if err != nil {
		return nil, err
	}
	return quoteDirectGas(ctx, g, b)
}

func quoteDirectGas(ctx context.Context, g *directGasContext, b DirectChildGasBinding) (*DirectChildGasPlan, error) {
	tokens, err := g.adapter.GetTokenBalance(ctx, g.child.Address, *g.token)
	if err != nil {
		return nil, err
	}
	if tokens == nil || tokens.Amount == nil || tokens.Amount.Cmp(g.amount) < 0 {
		return nil, ErrInsufficientFunds
	}
	fee, err := tokenTransferNativeFee(ctx, g.adapter, types.TransferRequest{From: g.child.Address, To: b.Recipient, Amount: g.amount, Asset: b.Asset, Token: g.token})
	if err != nil {
		return nil, err
	}
	seed := chain.EVMGasSeedAmount(fee)
	seedFee, err := tokenTransferNativeFee(ctx, g.adapter, types.TransferRequest{From: g.wallet.DepositAddress.Address, To: g.child.Address, Amount: seed, Asset: g.adapter.NativeAsset()})
	if err != nil {
		return nil, err
	}
	reserve := g.funderReserve()
	required := new(big.Int).Add(seed, seedFee)
	required.Add(required, reserve)
	balance, err := nativeGasBalance(ctx, g.adapter, g.wallet.DepositAddress.Address)
	if err != nil {
		return nil, err
	}
	if balance.Cmp(required) < 0 {
		return nil, ErrWalletNotGasReady
	}
	return &DirectChildGasPlan{BaseAddressID: g.wallet.DepositAddress.ID, BaseAddress: g.wallet.DepositAddress.Address,
		AmountWei: seed.String(), MaxFeeWei: seedFee.String(), MinFunderBalanceWei: reserve.String(), TokenFeeWei: fee.String(), ExpiresAt: time.Now().UTC().Add(time.Minute)}, nil
}

func validateDirectGasPlan(ctx context.Context, g *directGasContext, b DirectChildGasBinding, p DirectChildGasPlan) error {
	if b.OperationID == uuid.Nil || b.ClaimID == uuid.Nil || p.BaseAddressID != g.wallet.DepositAddress.ID || !strings.EqualFold(p.BaseAddress, g.wallet.DepositAddress.Address) || p.ExpiresAt.Before(time.Now()) || p.ExpiresAt.After(time.Now().Add(time.Hour)) {
		return ErrGasPreparationBinding
	}
	seed, err := gasPositive(p.AmountWei)
	if err != nil {
		return err
	}
	maxFee, err := gasPositive(p.MaxFeeWei)
	if err != nil {
		return err
	}
	reserve, err := gasNonnegative(p.MinFunderBalanceWei)
	if err != nil {
		return err
	}
	tokenBudget, err := gasPositive(p.TokenFeeWei)
	if err != nil {
		return err
	}
	if reserve.Cmp(g.funderReserve()) < 0 {
		return ErrGasPreparationBinding
	}
	fresh, err := quoteDirectGas(ctx, g, b)
	if err != nil {
		return err
	}
	needed, _ := gasPositive(fresh.AmountWei)
	fee, err := tokenTransferNativeFee(ctx, g.adapter, types.TransferRequest{From: p.BaseAddress, To: g.child.Address, Amount: seed, Asset: g.adapter.NativeAsset()})
	if err != nil {
		return err
	}
	currentToken, _ := gasPositive(fresh.TokenFeeWei)
	if seed.Cmp(needed) < 0 || fee.Cmp(maxFee) > 0 || currentToken.Cmp(tokenBudget) > 0 {
		return fmt.Errorf("%w: preparation fee envelope changed", ErrGasPreparationBinding)
	}
	balance, err := nativeGasBalance(ctx, g.adapter, p.BaseAddress)
	if err != nil {
		return err
	}
	required := new(big.Int).Add(seed, maxFee)
	required.Add(required, reserve)
	if balance.Cmp(required) < 0 {
		return ErrWalletNotGasReady
	}
	return nil
}
