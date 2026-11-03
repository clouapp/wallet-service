package solana

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/big"

	"github.com/gagliardetto/solana-go"
	lookup "github.com/gagliardetto/solana-go/programs/address-lookup-table"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

// Account creation and priority fees are bounded independently of input tokens.
const swapSolanaRentBudget uint64 = 10_000_000
const swapSolanaFeeBudget uint64 = 1_000_000

var _ chain.SolanaSwapBuilder = (*SolanaLive)(nil)

const swapLookupTableProgram = "AddressLookupTab1e1111111111111111111111111"

type swapAccount struct {
	Lamports uint64            `json:"lamports"`
	Owner    string            `json:"owner"`
	Data     []json.RawMessage `json:"data"`
}

func (a *swapAccount) binary() ([]byte, error) {
	if a == nil || len(a.Data) != 2 {
		return nil, fmt.Errorf("swap simulation: missing account data")
	}
	var encoded, encoding string
	if json.Unmarshal(a.Data[0], &encoded) != nil || json.Unmarshal(a.Data[1], &encoding) != nil || encoding != "base64" {
		return nil, fmt.Errorf("swap simulation: invalid account data")
	}
	return base64.StdEncoding.DecodeString(encoded)
}

// PrepareSolanaSwap accepts only the exact wallet as sole signer and fee payer.
// Simulation checks every message account, including address-table lookups, so
// another token balance or its delegation cannot be spent alongside this input.
func (a *SolanaLive) PrepareSolanaSwap(ctx context.Context, sender, encoded string, input *big.Int, token *types.Token) (*types.UnsignedTx, error) {
	if len(encoded) > 16_384 || input == nil || input.Sign() <= 0 || !input.IsUint64() {
		return nil, fmt.Errorf("invalid solana swap input")
	}
	tx, err := solana.TransactionFromBase64(encoded)
	if err != nil || tx.Message.Header.NumRequiredSignatures != 1 || len(tx.Message.AccountKeys) == 0 || tx.Message.AccountKeys[0].String() != sender {
		return nil, fmt.Errorf("solana swap must have the wallet as sole signer")
	}
	message, err := tx.Message.MarshalBinary()
	if err != nil {
		return nil, err
	}
	resolved := tx.Message
	if len(resolved.AddressTableLookups) > 0 {
		tables := map[solana.PublicKey]solana.PublicKeySlice{}
		for _, table := range resolved.AddressTableLookups {
			var result struct {
				Value *swapAccount `json:"value"`
			}
			if err := a.rpc.Call(ctx, "getAccountInfo", &result, table.AccountKey.String(), map[string]any{"encoding": "base64", "commitment": "confirmed"}); err != nil {
				return nil, err
			}
			if result.Value == nil || result.Value.Owner != swapLookupTableProgram {
				return nil, fmt.Errorf("invalid solana swap address table")
			}
			data, err := result.Value.binary()
			if err != nil {
				return nil, err
			}
			state, err := lookup.DecodeAddressLookupTableState(data)
			if err != nil {
				return nil, err
			}
			tables[table.AccountKey] = state.Addresses
		}
		if err := resolved.SetAddressTables(tables); err != nil {
			return nil, err
		}
		if err := resolved.ResolveLookups(); err != nil {
			return nil, err
		}
	}
	keys, err := resolved.GetAllKeys()
	if err != nil || len(keys) == 0 || len(keys) > 100 {
		return nil, fmt.Errorf("unsupported solana swap account count")
	}
	addresses := make([]string, len(keys))
	for i, key := range keys {
		addresses[i] = key.String()
	}
	var before struct {
		Context struct {
			Slot uint64 `json:"slot"`
		} `json:"context"`
		Value []*swapAccount `json:"value"`
	}
	if err := a.rpc.Call(ctx, "getMultipleAccounts", &before, addresses, map[string]any{"encoding": "base64", "commitment": "confirmed"}); err != nil {
		return nil, err
	}
	if len(before.Value) != len(keys) {
		return nil, fmt.Errorf("incomplete swap account snapshot")
	}
	var fee struct {
		Value *uint64 `json:"value"`
	}
	if err := a.rpc.Call(ctx, "getFeeForMessage", &fee, base64.StdEncoding.EncodeToString(message), map[string]any{"commitment": "confirmed"}); err != nil {
		return nil, err
	}
	if fee.Value == nil || *fee.Value > swapSolanaFeeBudget {
		return nil, fmt.Errorf("solana swap fee exceeds budget or blockhash expired")
	}
	var simulated struct {
		Value struct {
			Err      json.RawMessage `json:"err"`
			Accounts []*swapAccount  `json:"accounts"`
		} `json:"value"`
	}
	if err := a.rpc.Call(ctx, "simulateTransaction", &simulated, encoded, map[string]any{"encoding": "base64", "commitment": "confirmed", "sigVerify": false, "minContextSlot": before.Context.Slot, "accounts": map[string]any{"encoding": "base64", "addresses": addresses}}); err != nil {
		return nil, err
	}
	if len(simulated.Value.Err) > 0 && string(simulated.Value.Err) != "null" {
		return nil, fmt.Errorf("solana swap simulation failed")
	}
	if err := validateSwapAccounts(sender, input, token, addresses, before.Value, simulated.Value.Accounts, *fee.Value); err != nil {
		return nil, err
	}
	return &types.UnsignedTx{ChainID: a.cfg.ChainIDStr, RawBytes: message, Metadata: map[string]any{}, TransferAmount: new(big.Int).Set(input)}, nil
}

func validateSwapAccounts(sender string, input *big.Int, token *types.Token, addresses []string, before, after []*swapAccount, fee uint64) error {
	if len(before) != len(addresses) || len(after) != len(addresses) || len(addresses) == 0 || addresses[0] != sender {
		return fmt.Errorf("incomplete solana swap simulation")
	}
	base, post := before[0], after[0]
	if base == nil || post == nil || base.Owner != solana.SystemProgramID.String() || post.Owner != base.Owner {
		return fmt.Errorf("swap changes wallet ownership")
	}
	baseData, e1 := base.binary()
	postData, e2 := post.binary()
	if e1 != nil || e2 != nil || len(baseData) != 0 || len(postData) != 0 {
		return fmt.Errorf("swap changes wallet data")
	}
	nativeDebit := new(big.Int).Sub(new(big.Int).SetUint64(base.Lamports), new(big.Int).SetUint64(post.Lamports))
	nativeBudget := new(big.Int).SetUint64(fee + swapSolanaRentBudget)
	if token == nil {
		nativeBudget.Add(nativeBudget, input)
		if nativeDebit.Cmp(input) < 0 {
			return fmt.Errorf("swap does not debit the quoted native input")
		}
	}
	if nativeDebit.Cmp(nativeBudget) > 0 {
		return fmt.Errorf("swap exceeds native input and fee budget")
	}
	tokenDebit := new(big.Int)
	for i, account := range before {
		if account == nil || (account.Owner != solana.TokenProgramID.String() && account.Owner != solana.Token2022ProgramID.String()) {
			continue
		}
		data, err := account.binary()
		if err != nil {
			return err
		}
		if len(data) < 165 {
			continue
		}
		owner := solana.PublicKeyFromBytes(data[32:64]).String()
		if owner != sender {
			continue
		}
		next := after[i]
		if next == nil || next.Owner != account.Owner {
			return fmt.Errorf("swap closes or changes an existing wallet token account")
		}
		post, err := next.binary()
		if err != nil || len(post) != len(data) {
			return fmt.Errorf("swap changes token account layout")
		}
		if !bytes.Equal(data[:64], post[:64]) || !bytes.Equal(data[72:], post[72:]) {
			return fmt.Errorf("swap changes token ownership or delegation")
		}
		debit := new(big.Int).Sub(new(big.Int).SetUint64(binary.LittleEndian.Uint64(data[64:72])), new(big.Int).SetUint64(binary.LittleEndian.Uint64(post[64:72])))
		mint := solana.PublicKeyFromBytes(data[:32]).String()
		if debit.Sign() > 0 {
			if token == nil || mint != token.Contract || account.Owner != solana.TokenProgramID.String() {
				return fmt.Errorf("swap spends an unquoted token")
			}
			tokenDebit.Add(tokenDebit, debit)
		}
	}
	if token != nil && tokenDebit.Cmp(input) != 0 {
		return fmt.Errorf("swap token debit differs from quoted exact input")
	}
	return nil
}
