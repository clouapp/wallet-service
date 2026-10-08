// Package evmcall simulates and, on explicit request, broadcasts once a fully
// specified EVM call (e.g. an L1 → L2 bridge deposit) from a wallet's base address
// on an allowlisted EVM testnet. Every EVM network derives the same address from a
// secp256k1 key, so a wallet of one EVM chain can pay on another.
package evmcall

import (
	"errors"
	"fmt"
	"math/big"
)

// Network is an EVM testnet evm:call may sign for.
type Network struct {
	ChainID      int64
	Name         string
	NativeSymbol string
}

const (
	nativeDecimals = 18

	// MaxDataBytes, MaxGasLimit and MaxGasPriceWei bound what one call may encode.
	MaxDataBytes   = 16 << 10
	MaxGasLimit    = uint64(2_000_000)
	MaxGasPriceWei = int64(500_000_000_000) // 500 gwei

	gasLimitMarginPercent = 130
	gasPriceMultiplier    = 2 // same buffer the EVM adapter applies to eth_gasPrice
	percentDenominator    = 100
)

var (
	ErrMainnetRefused    = errors.New("mainnet chain ids are refused")
	ErrNetworkNotAllowed = errors.New("chain id is not an allowlisted EVM testnet")
)

// testnetNetworks are the only chain ids evm:call signs for: it funds testnets and
// must never produce a mainnet transaction.
var testnetNetworks = map[int64]Network{
	11155111: {ChainID: 11155111, Name: "Ethereum Sepolia", NativeSymbol: "ETH"},
	421614:   {ChainID: 421614, Name: "Arbitrum Sepolia", NativeSymbol: "ETH"},
	84532:    {ChainID: 84532, Name: "Base Sepolia", NativeSymbol: "ETH"},
	80002:    {ChainID: 80002, Name: "Polygon Amoy", NativeSymbol: "POL"},
	97:       {ChainID: 97, Name: "BNB Smart Chain Testnet", NativeSymbol: "tBNB"},
}

// knownMainnets get a dedicated error so a mainnet id is never mistaken for a typo.
var knownMainnets = map[int64]string{
	1:     "Ethereum",
	10:    "OP Mainnet",
	56:    "BNB Smart Chain",
	137:   "Polygon PoS",
	8453:  "Base",
	42161: "Arbitrum One",
	42170: "Arbitrum Nova",
	43114: "Avalanche C-Chain",
}

// TestnetNetwork returns the allowlisted testnet with chainID.
func TestnetNetwork(chainID int64) (Network, error) {
	if name, isMainnet := knownMainnets[chainID]; isMainnet {
		return Network{}, fmt.Errorf("%w: %d is %s", ErrMainnetRefused, chainID, name)
	}
	network, allowed := testnetNetworks[chainID]
	if !allowed {
		return Network{}, fmt.Errorf("%w: %d", ErrNetworkNotAllowed, chainID)
	}
	return network, nil
}

func maxGasPrice() *big.Int {
	return big.NewInt(MaxGasPriceWei)
}
