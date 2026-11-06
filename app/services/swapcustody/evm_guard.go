package swapcustody

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/macrowallets/waas/app/services/swap"
	"github.com/macrowallets/waas/pkg/types"
)

// lifiDiamonds is the LI.FI Diamond on each EVM network the catalog lists
// (li.quest /v1/chains diamondAddress, the same CREATE2 address on all five,
// checked 2026-11-04). LI.FI builds every EVM route as a call to its Diamond,
// which is also the approval spender, so any other target from LI.FI is refused.
var lifiDiamonds = map[int64]string{
	1:     "0x1231DEB6f5749EF6cE6943a275A1D3E7486F4EaE",
	56:    "0x1231DEB6f5749EF6cE6943a275A1D3E7486F4EaE",
	137:   "0x1231DEB6f5749EF6cE6943a275A1D3E7486F4EaE",
	8453:  "0x1231DEB6f5749EF6cE6943a275A1D3E7486F4EaE",
	42161: "0x1231DEB6f5749EF6cE6943a275A1D3E7486F4EaE",
}

// Functions that move or authorize balances the wallet already holds. A provider
// call never uses one on any contract: the only token call a route may make is
// the exact transfer of the quoted input to a deposit address (see guardEVMCall).
var forbiddenSignatures = []string{
	"transfer(address,uint256)",
	"approve(address,uint256)",
	"transferFrom(address,address,uint256)",
	"increaseAllowance(address,uint256)",
	"setApprovalForAll(address,bool)",
	"permit(address,address,uint256,uint256,uint8,bytes32,bytes32)",
	"permit(address,address,uint256,uint256,bool,uint8,bytes32,bytes32)",
	"safeTransferFrom(address,address,uint256)",
	"safeTransferFrom(address,address,uint256,bytes)",
	"safeTransferFrom(address,address,uint256,uint256,bytes)",
	"safeBatchTransferFrom(address,address,uint256[],uint256[],bytes)",
	"approve(address,address,uint160,uint48)",
	"transferFrom(address,address,uint160,address)",
	"transferFrom((address,address,uint160,address)[])",
	"permit(address,((address,uint160,uint48,uint48),address,uint256),bytes)",
	"permit(address,((address,uint160,uint48,uint48)[],address,uint256),bytes)",
	"lockdown((address,address)[])",
	"invalidateNonces(address,address,uint48)",
	// ERC-677/1363 transfer-and-call, legacy approvals, ERC-777 operators, DAI.
	"transferAndCall(address,uint256)",
	"transferAndCall(address,uint256,bytes)",
	"transferFromAndCall(address,address,uint256)",
	"transferFromAndCall(address,address,uint256,bytes)",
	"approveAndCall(address,uint256)",
	"approveAndCall(address,uint256,bytes)",
	"increaseApproval(address,uint256)",
	"send(address,uint256,bytes)",
	"operatorSend(address,address,uint256,bytes,bytes)",
	"authorizeOperator(address)",
	"push(address,uint256)",
	"pull(address,uint256)",
	"move(address,address,uint256)",
}

var (
	forbiddenSelectors = selectorSet(forbiddenSignatures)
	transferSelector   = selector("transfer(address,uint256)")
	// THORChain and Maya routers: depositWithExpiry(vault, asset, amount, memo, expiry).
	depositWithExpirySelector = selector("depositWithExpiry(address,address,uint256,string,uint256)")
	depositWithExpiryArgs     = mustArguments("address", "address", "uint256", "string", "uint256")
)

// guardEVMCall bounds what a provider-built EVM funding call can take from the
// wallet to the quoted input. A compromised or wrong provider can then at worst
// misroute that input; it can never move another balance of the wallet:
//
//   - LI.FI calls only its Diamond on that network, and approves only it;
//   - the call is never to a catalog token contract, except the source token's
//     exact transfer(depositAddress, input) (deposit-address routes);
//   - calldata never starts with a function that moves or authorizes balances;
//   - a token input is approved only to the contract being called;
//   - a THORChain/Maya router deposit carries the source asset, the exact input
//     and a swap memo paying the quote's destination.
func guardEVMCall(quote *swap.Quote, to string, value *big.Int, data []byte, token *types.Token, input *big.Int, approval string, catalog []types.Token) (directTransfer bool, err error) {
	from := quote.Request.FromAddress
	if !common.IsHexAddress(to) || common.HexToAddress(to) == (common.Address{}) || strings.EqualFold(to, from) {
		return false, fmt.Errorf("%w: swap call target", swap.ErrInvalidQuote)
	}
	if quote.Provider == swap.ProviderLiFi {
		diamond, known := lifiDiamonds[quote.Request.From.NetworkID]
		if !known || !strings.EqualFold(to, diamond) || (token != nil && !strings.EqualFold(approval, diamond)) {
			return false, fmt.Errorf("%w: LI.FI call outside its Diamond", swap.ErrInvalidQuote)
		}
	}
	for _, listed := range catalog {
		if listed.Contract == "" || !strings.EqualFold(listed.Contract, to) {
			continue
		}
		if token != nil && strings.EqualFold(token.Contract, to) && exactTransfer(data, input, from) && value.Sign() == 0 {
			return true, nil
		}
		return false, fmt.Errorf("%w: swap call targets a token contract", swap.ErrInvalidQuote)
	}
	if len(data) >= 4 {
		if name, forbidden := forbiddenSelectors[[4]byte(data[:4])]; forbidden {
			return false, fmt.Errorf("%w: swap calldata is %s", swap.ErrInvalidQuote, name)
		}
	}
	if token != nil && !strings.EqualFold(approval, to) {
		return false, fmt.Errorf("%w: approval spender differs from the called contract", swap.ErrInvalidQuote)
	}
	if len(data) >= 4 && bytes.Equal(data[:4], depositWithExpirySelector[:]) {
		if err := guardRouterDeposit(quote, data[4:], token, input); err != nil {
			return false, err
		}
	}
	return false, nil
}

// exactTransfer is transfer(recipient, input) with canonical ABI padding.
func exactTransfer(data []byte, input *big.Int, from string) bool {
	if len(data) != 68 || !bytes.Equal(data[:4], transferSelector[:]) {
		return false
	}
	for _, padding := range data[4:16] {
		if padding != 0 {
			return false
		}
	}
	recipient := common.BytesToAddress(data[16:36])
	return recipient != (common.Address{}) && !strings.EqualFold(recipient.Hex(), from) && new(big.Int).SetBytes(data[36:68]).Cmp(input) == 0
}

func guardRouterDeposit(quote *swap.Quote, encoded []byte, token *types.Token, input *big.Int) error {
	values, err := depositWithExpiryArgs.Unpack(encoded)
	if err != nil || len(values) != 5 {
		return fmt.Errorf("%w: router deposit does not decode", swap.ErrInvalidQuote)
	}
	asset, _ := values[1].(common.Address)
	quantity, _ := values[2].(*big.Int)
	memo, _ := values[3].(string)
	wantAsset := common.Address{}
	if token != nil {
		wantAsset = common.HexToAddress(token.Contract)
	}
	if asset != wantAsset || quantity == nil || quantity.Cmp(input) != 0 {
		return fmt.Errorf("%w: router deposit asset or amount differs from the quote", swap.ErrInvalidQuote)
	}
	return ValidateSwapMemo(memo, quote.Request)
}

// ValidateSwapMemo accepts only a THORChain/Maya swap memo whose destination is
// the quote's custody address (and refund, when named, its source address).
// Every other memo (liquidity, loans, trade accounts, unknown) is refused.
func ValidateSwapMemo(memo string, request swap.QuoteRequest) error {
	parts := strings.Split(strings.TrimSpace(memo), ":")
	if len(parts) < 3 {
		return fmt.Errorf("%w: swap memo", swap.ErrInvalidQuote)
	}
	switch strings.ToLower(parts[0]) {
	case "=", "s", "swap":
	default:
		return fmt.Errorf("%w: swap memo action", swap.ErrInvalidQuote)
	}
	destination, refund, hasRefund := strings.Cut(parts[2], "/")
	if parts[1] == "" || !sameMemoAddress(destination, request.ToAddress) || (hasRefund && !sameMemoAddress(refund, request.FromAddress)) {
		return fmt.Errorf("%w: swap memo destination differs from the quote", swap.ErrInvalidQuote)
	}
	if !memoAssetIs(parts[1], request.To) {
		return fmt.Errorf("%w: swap memo asset differs from the quote", swap.ErrInvalidQuote)
	}
	return nil
}

// thorchainPrefixes are the THORChain/Maya chain names of the custody chains.
var thorchainPrefixes = map[string]string{"eth": "ETH", "btc": "BTC", "ltc": "LTC", "bsc": "BSC", "base": "BASE", "arbitrum": "ARB", "sol": "SOL", "xrp": "XRP", "tron": "TRON", "polygon": "POL"}

// thorchainShorthands are the documented one-letter asset aliases custody accepts.
var thorchainShorthands = map[string]string{"b": "BTC.BTC", "e": "ETH.ETH", "l": "LTC.LTC"}

// memoAssetIs reports whether a memo's asset is the quote's destination asset:
// CHAIN.SYMBOL for a native coin, CHAIN.SYMBOL-CONTRACT (or a contract suffix)
// for a token. An unknown notation is refused, never guessed.
func memoAssetIs(asset string, to swap.Asset) bool {
	if alias, ok := thorchainShorthands[strings.ToLower(asset)]; ok {
		asset = alias
	}
	chainName, rest, ok := strings.Cut(strings.ToUpper(asset), ".")
	if !ok || chainName != thorchainPrefixes[to.Chain] {
		return false
	}
	symbol, contract, hasContract := strings.Cut(rest, "-")
	if symbol != strings.ToUpper(to.Symbol) {
		return false
	}
	if to.Native {
		return !hasContract
	}
	return hasContract && contract != "" && strings.HasSuffix(strings.ToUpper(to.Contract), contract)
}

// sameMemoAddress compares hex (EVM) and bech32 addresses case-insensitively,
// every other encoding (base58) exactly.
func sameMemoAddress(memo, address string) bool {
	if memo == "" || address == "" {
		return false
	}
	lower := strings.ToLower(address)
	if strings.HasPrefix(lower, "0x") || strings.HasPrefix(lower, "bc1") || strings.HasPrefix(lower, "tb1") || strings.HasPrefix(lower, "ltc1") || strings.HasPrefix(lower, "tltc1") {
		return strings.EqualFold(memo, address)
	}
	return memo == address
}

func selector(signature string) [4]byte {
	return [4]byte(crypto.Keccak256([]byte(signature))[:4])
}

func selectorSet(signatures []string) map[[4]byte]string {
	set := make(map[[4]byte]string, len(signatures))
	for _, signature := range signatures {
		set[selector(signature)] = signature[:strings.IndexByte(signature, '(')]
	}
	return set
}

func mustArguments(kinds ...string) abi.Arguments {
	arguments := make(abi.Arguments, 0, len(kinds))
	for _, kind := range kinds {
		typ, err := abi.NewType(kind, "", nil)
		if err != nil {
			panic(fmt.Sprintf("swap custody: abi type %s: %v", kind, err))
		}
		arguments = append(arguments, abi.Argument{Type: typ})
	}
	return arguments
}

// hexSelector is the call's first four bytes for logs (never arguments).
func hexSelector(data []byte) string {
	if len(data) < 4 {
		return "0x"
	}
	return "0x" + hex.EncodeToString(data[:4])
}
