package commands

import (
	"context"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"

	"github.com/macrowallets/waas/app/services/evmcall"
)

var evmCallRPCEnvPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// EVMCall simulates, and with --broadcast sends exactly once, an EVM call from a
// wallet's base address on an allowlisted EVM testnet (e.g. an L1 → L2 bridge
// deposit). A dev tool: mainnet chain ids are refused.
type EVMCall struct {
	wallets evmcall.WalletSource
	signer  evmcall.Signer
	runner  *evmcall.Runner
}

// EVMCallDeps is everything the evm:call command needs. Signer may be nil; it is
// used only when --broadcast is set.
type EVMCallDeps struct {
	Wallets evmcall.WalletSource
	Signer  evmcall.Signer
}

// NewEVMCall wires the command from EVMCallDeps.
func NewEVMCall(deps EVMCallDeps) *EVMCall {
	return &EVMCall{
		wallets: deps.Wallets,
		signer:  deps.Signer,
		runner:  &evmcall.Runner{Wallets: deps.Wallets, Signer: deps.Signer},
	}
}

type evmCallFlags struct {
	Wallet, ChainID, To, Data, Value, ValueWei, RPCEnv, GasLimit, Tag, ClaimDir string
	DryRun, Broadcast, PassphraseStdin, PassphraseVault                         bool
}

type evmCallInvocation struct {
	request         evmcall.Request
	rpcEnv          string
	broadcast       bool
	passphraseStdin bool
	claimDir        string
}

func (c *EVMCall) Signature() string {
	return "evm:call"
}

func (c *EVMCall) Description() string {
	return "Simulate (--dry-run) or send once (--broadcast) an EVM call from a wallet's base address on an EVM testnet"
}

func (c *EVMCall) Extend() command.Extend {
	return command.Extend{
		Category: "evm",
		Flags: []command.Flag{
			&command.StringFlag{Name: "wallet", Usage: "wallet UUID whose base address pays (secp256k1 / EVM)"},
			&command.StringFlag{Name: "chain-id", Usage: "EIP-155 chain id: 11155111, 421614, 84532, 80002 or 97 (mainnets are refused)"},
			&command.StringFlag{Name: "to", Usage: "destination or contract address (0x…)"},
			&command.StringFlag{Name: "data", Usage: "calldata as 0x-prefixed hex (default: none)"},
			&command.StringFlag{Name: "value", Usage: "amount of the native asset as a decimal, e.g. 0.03 (default 0)"},
			&command.StringFlag{Name: "value-wei", Usage: "amount in wei instead of --value"},
			&command.StringFlag{Name: "rpc-env", Usage: "name of the environment variable holding the RPC URL (the URL is never printed)"},
			&command.StringFlag{Name: "gas-limit", Usage: "gas limit; must cover eth_estimateGas (default: estimate + 30%)"},
			&command.StringFlag{Name: "tag", Usage: "unique name of this broadcast; a tag is claimed once and never reused"},
			&command.StringFlag{Name: "claim-dir", Usage: "directory of claim/result files (default: $" + evmcall.ClaimDirEnv + " or <e2e state dir>/locks)"},
			&command.BoolFlag{Name: "dry-run", Usage: "simulate and estimate only: no signing, no broadcast"},
			&command.BoolFlag{Name: "broadcast", Usage: "sign with MPC and send exactly once, then wait for the receipt"},
			&command.BoolFlag{Name: "passphrase-stdin", Usage: "read the wallet passphrase from the first line of stdin"},
			&command.BoolFlag{Name: "passphrase-vault", Usage: "read the verified passphrase from the encrypted e2e wallet vault"},
		},
	}
}

func (c *EVMCall) Handle(ctx console.Context) error {
	invocation, err := parseEVMCallFlags(readEVMCallFlags(ctx))
	if err != nil {
		return fail(ctx, err)
	}
	lines, err := c.runner.Execute(context.Background(), evmcall.RunRequest{
		Request:         invocation.request,
		RPCEnv:          invocation.rpcEnv,
		Broadcast:       invocation.broadcast,
		PassphraseStdin: invocation.passphraseStdin,
		ClaimDir:        invocation.claimDir,
	})
	for _, line := range lines {
		ctx.Info(redactedLine(line))
	}
	if err != nil {
		return fail(ctx, err)
	}
	return nil
}

func readEVMCallFlags(ctx console.Context) evmCallFlags {
	return evmCallFlags{
		Wallet: ctx.Option("wallet"), ChainID: ctx.Option("chain-id"), To: ctx.Option("to"), Data: ctx.Option("data"),
		Value: ctx.Option("value"), ValueWei: ctx.Option("value-wei"), RPCEnv: ctx.Option("rpc-env"),
		GasLimit: ctx.Option("gas-limit"), Tag: ctx.Option("tag"), ClaimDir: ctx.Option("claim-dir"),
		DryRun: ctx.OptionBool("dry-run"), Broadcast: ctx.OptionBool("broadcast"),
		PassphraseStdin: ctx.OptionBool("passphrase-stdin"), PassphraseVault: ctx.OptionBool("passphrase-vault"),
	}
}

// parseEVMCallFlags validates every flag before anything touches a node or the database.
func parseEVMCallFlags(flags evmCallFlags) (evmCallInvocation, error) {
	if flags.DryRun == flags.Broadcast {
		return evmCallInvocation{}, fmt.Errorf("pass exactly one of --dry-run or --broadcast")
	}
	walletID, err := uuid.Parse(strings.TrimSpace(flags.Wallet))
	if err != nil {
		return evmCallInvocation{}, fmt.Errorf("--wallet must be a wallet UUID")
	}
	chainID, err := strconv.ParseInt(strings.TrimSpace(flags.ChainID), 10, 64)
	if err != nil {
		return evmCallInvocation{}, fmt.Errorf("--chain-id must be an integer")
	}
	if _, err := evmcall.TestnetNetwork(chainID); err != nil {
		return evmCallInvocation{}, err
	}
	data, err := evmcall.DecodeCallData(flags.Data)
	if err != nil {
		return evmCallInvocation{}, fmt.Errorf("--data: %w", err)
	}
	value, err := parseEVMCallValue(flags.Value, flags.ValueWei)
	if err != nil {
		return evmCallInvocation{}, err
	}
	rpcEnv := strings.TrimSpace(flags.RPCEnv)
	if !evmCallRPCEnvPattern.MatchString(rpcEnv) {
		return evmCallInvocation{}, fmt.Errorf("--rpc-env must name an environment variable (e.g. SEPOLIA_RPC_URL), not a URL")
	}
	gasLimit, err := parseEVMCallGasLimit(flags.GasLimit)
	if err != nil {
		return evmCallInvocation{}, err
	}

	invocation := evmCallInvocation{
		request: evmcall.Request{
			WalletID: walletID, ChainID: chainID, To: strings.TrimSpace(flags.To), Data: data,
			Value: value, GasLimit: gasLimit, Tag: strings.TrimSpace(flags.Tag),
		},
		rpcEnv: rpcEnv, broadcast: flags.Broadcast, passphraseStdin: flags.PassphraseStdin,
		claimDir: strings.TrimSpace(flags.ClaimDir),
	}
	if err := invocation.request.Validate(); err != nil {
		return evmCallInvocation{}, err
	}
	if err := checkEVMCallModeFlags(flags, invocation.request.Tag); err != nil {
		return evmCallInvocation{}, err
	}
	return invocation, nil
}

func checkEVMCallModeFlags(flags evmCallFlags, tag string) error {
	if flags.DryRun {
		if flags.PassphraseStdin || flags.PassphraseVault {
			return fmt.Errorf("--dry-run never signs; drop the passphrase flag")
		}
		return nil
	}
	if tag == "" {
		return fmt.Errorf("--broadcast needs --tag")
	}
	if flags.PassphraseStdin == flags.PassphraseVault {
		return fmt.Errorf("--broadcast needs exactly one of --passphrase-stdin or --passphrase-vault")
	}
	return nil
}

func parseEVMCallValue(value, valueWei string) (*big.Int, error) {
	value, valueWei = strings.TrimSpace(value), strings.TrimSpace(valueWei)
	switch {
	case value != "" && valueWei != "":
		return nil, fmt.Errorf("pass --value or --value-wei, not both")
	case valueWei != "":
		return evmcall.ParseWei(valueWei)
	case value != "":
		return evmcall.ParseNativeAmount(value)
	default:
		return new(big.Int), nil
	}
}

func parseEVMCallGasLimit(raw string) (uint64, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, nil
	}
	gasLimit, err := strconv.ParseUint(trimmed, 10, 64)
	if err != nil || gasLimit == 0 {
		return 0, fmt.Errorf("--gas-limit must be a positive integer")
	}
	return gasLimit, nil
}
