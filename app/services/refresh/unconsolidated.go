package refresh

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/amount"
	"github.com/macrowallets/waas/pkg/types"
)

// AddressLedgerStore sums what the recorded transactions of one wallet moved, per
// address and asset, and what its outbound rows paid in fees.
type AddressLedgerStore interface {
	WalletAddressLedger(ctx context.Context, walletID uuid.UUID, chainID string) ([]models.AddressLedgerEntry, error)
	WalletAddressFees(ctx context.Context, walletID uuid.UUID, chainID string, withAccountRent bool) ([]models.AddressFeeEntry, error)
}

// nativeAssetKey keys the native asset among the token contracts.
const nativeAssetKey = ""

// unconsolidatedHolding is what the wallet's deposit addresses other than the base
// hold of one asset: the sum and how many addresses hold some.
type unconsolidatedHolding struct {
	amount    *big.Int
	addresses int
}

// unconsolidated reads what the wallet's deposit addresses other than the base hold,
// keyed by the registered token contract (nativeAssetKey for the native asset).
//
// The ledger picks what to read: an address and asset whose recorded rows leave a
// positive amount (confirmed deposits, sweeps and gas seeds in, less withdrawals,
// sweeps and gas seeds out and, for the native asset, the fees they paid). Only
// those are read on chain, so a consolidated address costs no call however many
// addresses the wallet has. A read that fails counts the ledger's amount instead,
// and once the provider rate limits, the rest of the pass does too: a recorded
// deposit is never left out of the total because a read failed. A transfer the
// scanner never recorded is not seen, as it is not in the history either.
func (s *BalanceService) unconsolidated(ctx context.Context, wallet *models.Wallet, adapter types.Chain) (map[string]*unconsolidatedHolding, error) {
	if s.ledger == nil {
		return nil, nil
	}
	entries, err := s.ledger.WalletAddressLedger(ctx, wallet.ID, wallet.Chain)
	if err != nil {
		return nil, fmt.Errorf("read the deposit address ledger: %w", err)
	}
	_, recordsRent := adapter.(chain.AccountRentReader)
	fees, err := s.ledger.WalletAddressFees(ctx, wallet.ID, wallet.Chain, recordsRent)
	if err != nil {
		return nil, fmt.Errorf("read the deposit address fees: %w", err)
	}

	base := wallet.DepositAddress.Address
	// net[address][asset key]: what the ledger leaves on each address. Contracts are
	// keyed as registered, so an EVM contract recorded in two cases is one asset.
	net := map[string]map[string]*big.Int{}
	var order []string
	tokens := map[string]types.Token{}
	for _, entry := range entries {
		if entry.Address == "" || entry.Address == base {
			continue
		}
		key := nativeAssetKey
		if entry.TokenContract != "" {
			token, err := s.registry.FindTokenByContract(wallet.Chain, entry.TokenContract)
			if err != nil || token == nil {
				// A token the chain does not list has no balance row to add it to.
				continue
			}
			key = token.Contract
			tokens[key] = *token
		}
		credited, okIn := amount.ParseBaseUnits(entry.Credited)
		debited, okOut := amount.ParseBaseUnits(entry.Debited)
		if !okIn || !okOut {
			slog.Warn("skipping an unreadable deposit address ledger entry", "wallet", wallet.ID, "chain", wallet.Chain, "asset", entry.Asset)
			continue
		}
		if net[entry.Address] == nil {
			net[entry.Address] = map[string]*big.Int{}
			order = append(order, entry.Address)
		}
		left := net[entry.Address][key]
		if left == nil {
			left = new(big.Int)
			net[entry.Address][key] = left
		}
		left.Add(left, credited.Sub(credited, debited))
	}
	for _, fee := range fees {
		paid, ok := amount.ParseBaseUnits(fee.Fees)
		if !ok || net[fee.Address] == nil || net[fee.Address][nativeAssetKey] == nil {
			continue
		}
		native := net[fee.Address][nativeAssetKey]
		native.Sub(native, paid)
	}

	holdings := map[string]*unconsolidatedHolding{}
	rateLimited := false
	for _, address := range order {
		for key, left := range net[address] {
			if left.Sign() <= 0 {
				continue
			}
			held := left
			if !rateLimited {
				read, err := readHeld(ctx, adapter, address, key, tokens)
				switch {
				case err == nil:
					held = read
				case errors.Is(err, chain.ErrRateLimited):
					rateLimited = true
					slog.Warn("deposit address balances rate limited; the ledger stands in for the rest of the pass", "wallet", wallet.ID, "chain", wallet.Chain)
				default:
					slog.Warn("deposit address balance not read; the ledger stands in", "wallet", wallet.ID, "chain", wallet.Chain, "error", err)
				}
			}
			if held.Sign() <= 0 {
				continue
			}
			holding := holdings[key]
			if holding == nil {
				holding = &unconsolidatedHolding{amount: new(big.Int)}
				holdings[key] = holding
			}
			holding.amount.Add(holding.amount, held)
			holding.addresses++
		}
	}
	return holdings, nil
}

// readHeld reads what address holds of the asset under key on chain.
func readHeld(ctx context.Context, adapter types.Chain, address, key string, tokens map[string]types.Token) (*big.Int, error) {
	var balance *types.Balance
	var err error
	if key == nativeAssetKey {
		balance, err = adapter.GetBalance(ctx, address)
	} else {
		balance, err = adapter.GetTokenBalance(ctx, address, tokens[key])
	}
	if err != nil {
		return nil, err
	}
	if balance == nil || balance.Amount == nil {
		return nil, errors.New("the chain answered no amount")
	}
	if balance.Amount.Sign() < 0 {
		return nil, amount.ErrNegativeAmount
	}
	return balance.Amount, nil
}

// withUnconsolidated sets on row what the other deposit addresses hold of its asset.
func withUnconsolidated(row *models.WalletAssetBalance, holding *unconsolidatedHolding) {
	row.UnconsolidatedRaw, row.UnconsolidatedDisplay, row.UnconsolidatedAddresses = "0", "0", 0
	if holding == nil || holding.amount.Sign() <= 0 {
		return
	}
	row.UnconsolidatedRaw = holding.amount.String()
	row.UnconsolidatedDisplay = amount.FormatBaseUnits(holding.amount, row.Decimals)
	row.UnconsolidatedAddresses = holding.addresses
}
