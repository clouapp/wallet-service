package main

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"strings"
	"time"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

const (
	privateKeyEnvironmentVariable = "E2E_FUNDER_PRIVATE_KEY"
	defaultSendTimeout            = 180 * time.Second
	receiptPollInterval           = 2 * time.Second
	nativeTransferGasLimit        = uint64(21_000)
	etherDecimals                 = 18
	gasPriceMultiplier            = int64(2)
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: e2e-funder <generate|address|send>")
	}

	switch args[0] {
	case "generate":
		return generateKey()
	case "address":
		return printAddress()
	case "send":
		return send(args[1:])
	default:
		return fmt.Errorf("unknown command %q; expected generate, address, or send", args[0])
	}
}

func generateKey() error {
	privateKey, err := crypto.GenerateKey()
	if err != nil {
		return fmt.Errorf("generate private key: %w", err)
	}
	fmt.Printf("%s=0x%s\n", privateKeyEnvironmentVariable, hex.EncodeToString(crypto.FromECDSA(privateKey)))
	fmt.Printf("address=%s\n", crypto.PubkeyToAddress(privateKey.PublicKey).Hex())
	return nil
}

func printAddress() error {
	privateKey, err := privateKeyFromEnvironment()
	if err != nil {
		return err
	}
	fmt.Println(crypto.PubkeyToAddress(privateKey.PublicKey).Hex())
	return nil
}

func send(args []string) error {
	flags := flag.NewFlagSet("send", flag.ContinueOnError)
	rpcURL := flags.String("rpc", "", "Sepolia RPC URL")
	destination := flags.String("to", "", "destination EVM address")
	amount := flags.String("amount", "", "amount in ETH")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := validateRPCURL(*rpcURL); err != nil {
		return err
	}
	if !common.IsHexAddress(*destination) {
		return fmt.Errorf("invalid destination address %q", *destination)
	}

	value, err := parseEther(*amount)
	if err != nil {
		return err
	}
	privateKey, err := privateKeyFromEnvironment()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultSendTimeout)
	defer cancel()

	client, err := ethclient.DialContext(ctx, *rpcURL)
	if err != nil {
		return fmt.Errorf("connect to RPC: %w", err)
	}
	defer client.Close()

	sender := crypto.PubkeyToAddress(privateKey.PublicKey)
	nonce, err := client.PendingNonceAt(ctx, sender)
	if err != nil {
		return fmt.Errorf("load sender nonce: %w", err)
	}
	chainID, err := client.ChainID(ctx)
	if err != nil {
		return fmt.Errorf("load chain id: %w", err)
	}
	header, err := client.HeaderByNumber(ctx, nil)
	if err != nil {
		return fmt.Errorf("load latest block header: %w", err)
	}
	tipSuggestion, err := client.SuggestGasTipCap(ctx)
	if err != nil {
		return fmt.Errorf("suggest priority fee: %w", err)
	}
	feeCap, tipCap, err := dynamicFeeCaps(header.BaseFee, tipSuggestion)
	if err != nil {
		return err
	}
	fee := new(big.Int).Mul(new(big.Int).SetUint64(nativeTransferGasLimit), feeCap)
	required := new(big.Int).Add(new(big.Int).Set(value), fee)
	balance, err := client.BalanceAt(ctx, sender, nil)
	if err != nil {
		return fmt.Errorf("load funder balance: %w", err)
	}
	if balance.Cmp(required) < 0 {
		return fmt.Errorf(
			"insufficient funder balance: have %s wei, need at least %s wei",
			balance.String(),
			required.String(),
		)
	}

	to := common.HexToAddress(*destination)
	unsigned := types.NewTx(&types.DynamicFeeTx{
		ChainID:   chainID,
		Nonce:     nonce,
		GasTipCap: tipCap,
		GasFeeCap: feeCap,
		Gas:       nativeTransferGasLimit,
		To:        &to,
		Value:     value,
	})
	signed, err := types.SignTx(unsigned, types.LatestSignerForChainID(chainID), privateKey)
	if err != nil {
		return fmt.Errorf("sign transfer: %w", err)
	}
	if err := client.SendTransaction(ctx, signed); err != nil {
		return fmt.Errorf("broadcast transfer: %w", err)
	}
	if err := waitForReceipt(ctx, client, signed.Hash()); err != nil {
		return err
	}
	fmt.Println(signed.Hash().Hex())
	return nil
}

func bufferedGasPrice(suggested *big.Int) *big.Int {
	if suggested == nil || suggested.Sign() <= 0 {
		return new(big.Int)
	}
	return new(big.Int).Mul(new(big.Int).Set(suggested), big.NewInt(gasPriceMultiplier))
}

func dynamicFeeCaps(baseFee, suggestedTip *big.Int) (*big.Int, *big.Int, error) {
	if baseFee == nil || baseFee.Sign() <= 0 {
		return nil, nil, fmt.Errorf("latest block has no positive base fee")
	}
	if suggestedTip == nil || suggestedTip.Sign() <= 0 {
		return nil, nil, fmt.Errorf("RPC returned no positive priority fee")
	}
	tipCap := new(big.Int).Set(suggestedTip)
	feeCap := new(big.Int).Mul(new(big.Int).Set(baseFee), big.NewInt(gasPriceMultiplier))
	feeCap.Add(feeCap, tipCap)
	return feeCap, tipCap, nil
}

func privateKeyFromEnvironment() (*ecdsa.PrivateKey, error) {
	value := strings.TrimSpace(os.Getenv(privateKeyEnvironmentVariable))
	if value == "" {
		return nil, fmt.Errorf("%s is required", privateKeyEnvironmentVariable)
	}
	return parsePrivateKey(value)
}

func parsePrivateKey(value string) (*ecdsa.PrivateKey, error) {
	normalized := strings.TrimPrefix(strings.TrimSpace(value), "0x")
	if len(normalized) != 64 {
		return nil, fmt.Errorf("private key must contain exactly 32 bytes")
	}
	privateKey, err := crypto.HexToECDSA(normalized)
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}
	return privateKey, nil
}

func parseEther(value string) (*big.Int, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, fmt.Errorf("amount is required")
	}
	parts := strings.Split(trimmed, ".")
	if len(parts) > 2 {
		return nil, fmt.Errorf("invalid ETH amount %q", value)
	}
	whole := parts[0]
	if whole == "" {
		whole = "0"
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	if !containsOnlyDigits(whole) || !containsOnlyDigits(fraction) {
		return nil, fmt.Errorf("invalid ETH amount %q", value)
	}
	if len(fraction) > etherDecimals {
		return nil, fmt.Errorf("ETH amount has more than %d decimal places", etherDecimals)
	}
	fraction += strings.Repeat("0", etherDecimals-len(fraction))
	raw := strings.TrimLeft(whole+fraction, "0")
	if raw == "" {
		return nil, fmt.Errorf("ETH amount must be greater than zero")
	}
	result, ok := new(big.Int).SetString(raw, 10)
	if !ok {
		return nil, fmt.Errorf("invalid ETH amount %q", value)
	}
	return result, nil
}

func containsOnlyDigits(value string) bool {
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func validateRPCURL(value string) error {
	parsed, err := url.ParseRequestURI(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("invalid RPC URL")
	}
	return nil
}

type receiptClient interface {
	TransactionReceipt(ctx context.Context, txHash common.Hash) (*types.Receipt, error)
}

func waitForReceipt(ctx context.Context, client receiptClient, transactionHash common.Hash) error {
	ticker := time.NewTicker(receiptPollInterval)
	defer ticker.Stop()

	for {
		receipt, err := client.TransactionReceipt(ctx, transactionHash)
		switch {
		case err == nil && receipt.Status == types.ReceiptStatusSuccessful:
			return nil
		case err == nil:
			return fmt.Errorf("transaction %s reverted", transactionHash.Hex())
		case !errors.Is(err, ethereum.NotFound):
			return fmt.Errorf("load transaction receipt: %w", err)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for transaction %s: %w", transactionHash.Hex(), ctx.Err())
		case <-ticker.C:
		}
	}
}
