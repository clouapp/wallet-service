package swap

import (
	"context"
	"encoding/hex"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/amount"
	"github.com/macrowallets/waas/pkg/types"
)

// Deposits reads credits already independently observed by the chain scanner and
// finalized by its confirmation policy, rather than trusting provider amounts.
type Deposits interface {
	FindDepositsByChainAndTxHash(context.Context, string, string) ([]models.Transaction, error)
}

// CreditClaims allocates funds under a durable transaction-level lock. The
// confirmed total is read inside that same transaction, never supplied by a provider.
type CreditClaims interface {
	ClaimCredit(context.Context, *models.SwapCreditAllocation, func(context.Context) (string, error)) (string, bool, error)
}

type DepositVerifier struct {
	deposits Deposits
	claims   CreditClaims
}

func NewDepositVerifier(deposits Deposits, claims CreditClaims) *DepositVerifier {
	return &DepositVerifier{deposits: deposits, claims: claims}
}

func (v *DepositVerifier) Credit(ctx context.Context, request CreditRequest) (string, bool, error) {
	units, ok := amount.ParseBaseUnits(request.ReportedRaw)
	if v.claims == nil || request.OrderID == uuid.Nil || request.WalletID == uuid.Nil || request.Hash == "" || !ok || units.Sign() <= 0 || len(request.ReportedRaw) > 78 || (request.Direction != "receive" && request.Direction != "refund") {
		return "0", false, nil
	}
	assetKey, address, hash := "native", request.Address, request.Hash
	if !request.Asset.Native {
		assetKey = "token:" + request.Asset.Contract
	}
	if request.Asset.NetworkID > 0 && request.Asset.Chain != "tron" {
		assetKey, address = strings.ToLower(assetKey), strings.ToLower(address)
	}
	hash = canonicalHash(hash)
	allocation := &models.SwapCreditAllocation{ID: uuid.New(), OrderID: request.OrderID, WalletID: request.WalletID, Direction: request.Direction, Chain: request.Asset.Chain, AssetKey: assetKey, Address: address, TxHash: hash, AmountRaw: units.String()}
	return v.claims.ClaimCredit(ctx, allocation, func(txCtx context.Context) (string, error) {
		return v.confirmedTotal(txCtx, request.WalletID, request.Asset, request.Address, request.Hash, request.NotBefore)
	})
}

// canonicalHash spells a 32-byte hex hash one way (lower case; a 0x/0X prefix
// becomes 0x), so two spellings of one payout can never hold two allocations:
// EVM hashes and the unprefixed BTC, LTC, XRP and TRON ids alike. Other
// encodings (Solana base58) are case-sensitive and kept as given.
func canonicalHash(hash string) string {
	body, prefixed := hash, false
	if len(hash) > 2 && (hash[:2] == "0x" || hash[:2] == "0X") {
		body, prefixed = hash[2:], true
	}
	if decoded, err := hex.DecodeString(body); err != nil || len(decoded) != 32 {
		return hash
	}
	if prefixed {
		return "0x" + strings.ToLower(body)
	}
	return strings.ToLower(body)
}

func (v *DepositVerifier) confirmedTotal(ctx context.Context, walletID uuid.UUID, asset Asset, address, hash string, notBefore time.Time) (string, error) {
	rows, err := v.deposits.FindDepositsByChainAndTxHash(ctx, asset.Chain, hash)
	if err != nil {
		return "0", err
	}
	total := new(big.Int)
	for _, row := range rows {
		contract := row.TokenContract == asset.Contract
		matchesAddress := row.ToAddress == address
		if asset.NetworkID > 0 && asset.Chain != "tron" {
			contract = strings.EqualFold(row.TokenContract, asset.Contract)
			matchesAddress = strings.EqualFold(row.ToAddress, address)
		}
		if row.WalletID != walletID || !matchesAddress || !contract || !strings.EqualFold(row.Asset, asset.Symbol) {
			continue
		}
		if !notBefore.IsZero() && (row.CreatedAt == nil || row.CreatedAt.StdTime().Before(notBefore)) {
			// Recorded before the order existed: another deposit, never this payout.
			continue
		}
		if row.Status != string(types.TxStatusConfirmed) {
			return "0", nil
		}
		units, ok := amount.ParseBaseUnits(row.Amount)
		if !ok || units.Sign() < 0 {
			return "0", ErrInvalidRequest
		}
		total.Add(total, units)
	}
	return total.String(), nil
}
