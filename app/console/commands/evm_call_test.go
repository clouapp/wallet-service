package commands

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/evmcall"
	"github.com/macrowallets/waas/app/services/sweep"
	"github.com/macrowallets/waas/pkg/types"
)

var _ evmcall.Signer = sweep.EVMCallPreflighter(nil)

type evmCallWalletStub struct{}

func (evmCallWalletStub) FindByID(context.Context, uuid.UUID) (*models.Wallet, error) {
	return nil, nil
}

type evmCallSignerStub struct{}

func (evmCallSignerStub) PreflightEVMCall(context.Context, uuid.UUID, string, types.Chain, *types.UnsignedTx) (*types.SignedTx, error) {
	return nil, nil
}

func TestNewEVMCallKeepsItsDependencies(t *testing.T) {
	wallets := evmCallWalletStub{}
	signer := evmCallSignerStub{}
	cmd := NewEVMCall(EVMCallDeps{Wallets: wallets, Signer: signer})
	if cmd == nil {
		t.Fatal("NewEVMCall returned nil")
	}
	if cmd.wallets != wallets {
		t.Fatal("evm call did not keep the wallet source")
	}
	if cmd.signer != signer {
		t.Fatal("evm call did not keep the signer")
	}
}

func TestNewEVMCallAllowsANilSigner(t *testing.T) {
	wallets := evmCallWalletStub{}
	cmd := NewEVMCall(EVMCallDeps{Wallets: wallets})
	if cmd == nil {
		t.Fatal("NewEVMCall returned nil")
	}
	if cmd.wallets != wallets {
		t.Fatal("evm call did not keep the wallet source")
	}
	if cmd.signer != nil {
		t.Fatal("signer should stay nil when omitted")
	}
}

const evmCallTestWallet = "c61f1974-5720-4eeb-9bd6-7f80ce217dd4"

func validEVMCallDryRun() evmCallFlags {
	return evmCallFlags{
		Wallet: evmCallTestWallet, ChainID: "11155111", To: "0xaAe29B0366299461418F5324a79Afc425BE5ae21",
		Data: "0x439370b1", Value: "0.03", RPCEnv: "SEPOLIA_RPC_URL", DryRun: true,
	}
}

func validEVMCallBroadcast() evmCallFlags {
	flags := validEVMCallDryRun()
	flags.DryRun, flags.Broadcast, flags.Tag, flags.PassphraseVault = false, true, "bridge-test-1", true
	return flags
}

func TestParseEVMCallFlags_AcceptsADryRunAndABroadcast(t *testing.T) {
	dryRun, err := parseEVMCallFlags(validEVMCallDryRun())
	if err != nil {
		t.Fatal(err)
	}
	if dryRun.broadcast || dryRun.request.Value.String() != "30000000000000000" || len(dryRun.request.Data) != 4 || dryRun.rpcEnv != "SEPOLIA_RPC_URL" {
		t.Fatalf("dry run %+v", dryRun)
	}
	broadcast, err := parseEVMCallFlags(validEVMCallBroadcast())
	if err != nil {
		t.Fatal(err)
	}
	if !broadcast.broadcast || broadcast.request.Tag != "bridge-test-1" {
		t.Fatalf("broadcast %+v", broadcast)
	}
}

func TestParseEVMCallFlags_ValueInWeiOrDefaultZero(t *testing.T) {
	flags := validEVMCallDryRun()
	flags.Value, flags.ValueWei, flags.Data = "", "12345", ""
	invocation, err := parseEVMCallFlags(flags)
	if err != nil || invocation.request.Value.String() != "12345" || invocation.request.Data != nil {
		t.Fatalf("invocation %+v err %v", invocation, err)
	}
	flags.ValueWei = ""
	invocation, err = parseEVMCallFlags(flags)
	if err != nil || invocation.request.Value.Sign() != 0 {
		t.Fatalf("default value %+v err %v", invocation.request.Value, err)
	}
	flags.GasLimit = "150000"
	if invocation, err = parseEVMCallFlags(flags); err != nil || invocation.request.GasLimit != 150_000 {
		t.Fatalf("gas limit %d err %v", invocation.request.GasLimit, err)
	}
}

func TestParseEVMCallFlags_RefusesMainnets(t *testing.T) {
	for _, chainID := range []string{"1", "56", "42161", "8453", "137", "10"} {
		flags := validEVMCallDryRun()
		flags.ChainID = chainID
		if _, err := parseEVMCallFlags(flags); !errors.Is(err, evmcall.ErrMainnetRefused) {
			t.Errorf("chain %s: %v", chainID, err)
		}
	}
}

func TestParseEVMCallFlags_RejectsBadFlags(t *testing.T) {
	cases := map[string]func(f *evmCallFlags){
		"no mode":                 func(f *evmCallFlags) { f.DryRun = false },
		"both modes":              func(f *evmCallFlags) { f.Broadcast = true },
		"wallet not a uuid":       func(f *evmCallFlags) { f.Wallet = "arbitrum_withdraw" },
		"chain id not a number":   func(f *evmCallFlags) { f.ChainID = "sepolia" },
		"unlisted chain id":       func(f *evmCallFlags) { f.ChainID = "5" },
		"bad destination":         func(f *evmCallFlags) { f.To = "0x1234" },
		"unprefixed data":         func(f *evmCallFlags) { f.Data = "439370b1" },
		"odd hex data":            func(f *evmCallFlags) { f.Data = "0x439370b" },
		"value and value-wei":     func(f *evmCallFlags) { f.ValueWei = "1" },
		"negative value":          func(f *evmCallFlags) { f.Value = "-0.1" },
		"too many decimals":       func(f *evmCallFlags) { f.Value = "0.0000000000000000001" },
		"rpc url instead of env":  func(f *evmCallFlags) { f.RPCEnv = "https://eth-sepolia.example/v2/key" },
		"lowercase env name":      func(f *evmCallFlags) { f.RPCEnv = "sepolia_rpc" },
		"no rpc env":              func(f *evmCallFlags) { f.RPCEnv = "" },
		"zero gas limit":          func(f *evmCallFlags) { f.GasLimit = "0" },
		"gas limit not a number":  func(f *evmCallFlags) { f.GasLimit = "lots" },
		"gas limit over the cap":  func(f *evmCallFlags) { f.GasLimit = "30000000" },
		"dry run with passphrase": func(f *evmCallFlags) { f.PassphraseStdin = true },
		"dry run with vault":      func(f *evmCallFlags) { f.PassphraseVault = true },
	}
	for name, mutate := range cases {
		flags := validEVMCallDryRun()
		mutate(&flags)
		if _, err := parseEVMCallFlags(flags); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestParseEVMCallFlags_BroadcastNeedsATagAndOnePassphraseSource(t *testing.T) {
	cases := map[string]func(f *evmCallFlags){
		"no tag":                func(f *evmCallFlags) { f.Tag = "" },
		"no passphrase source":  func(f *evmCallFlags) { f.PassphraseVault = false },
		"two passphrase source": func(f *evmCallFlags) { f.PassphraseStdin = true },
	}
	for name, mutate := range cases {
		flags := validEVMCallBroadcast()
		mutate(&flags)
		if _, err := parseEVMCallFlags(flags); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestParseEVMCallFlags_ErrorsNeverEchoTheRPCURL(t *testing.T) {
	flags := validEVMCallDryRun()
	flags.RPCEnv = "https://eth-sepolia.g.alchemy.com/v2/secret-key"
	_, err := parseEVMCallFlags(flags)
	if err == nil || strings.Contains(err.Error(), "secret-key") {
		t.Fatalf("err %v", err)
	}
}

func TestResolveEVMCallClaimDir(t *testing.T) {
	if dir, err := evmcall.ResolveClaimDir("/tmp/claims"); err != nil || dir != "/tmp/claims" {
		t.Fatalf("flag: %s %v", dir, err)
	}
	t.Setenv(evmcall.ClaimDirEnv, "/tmp/env-claims")
	if dir, _ := evmcall.ResolveClaimDir(""); dir != "/tmp/env-claims" {
		t.Fatalf("env: %s", dir)
	}
	t.Setenv(evmcall.ClaimDirEnv, "")
	t.Setenv(evmcall.StateDirEnv, "/tmp/state")
	if dir, _ := evmcall.ResolveClaimDir(""); dir != "/tmp/state/locks" {
		t.Fatalf("state dir: %s", dir)
	}
}
