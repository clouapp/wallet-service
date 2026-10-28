//go:build solana_signatures_live

package solana

// Read-only check against Solana mainnet (go test -tags solana_signatures_live -run
// TestLive_Mainnet ./app/adapters/chain/solana/; SOLANA_MAINNET_LIVE_RPC_URL points it
// at another mainnet endpoint), with the tokens the catalog registers on mainnet: the
// accounts the signature sync reads for a production deposit address (the address and
// its associated account of every token, none skipped), the credit of a deposit made
// to it on 2026-10-28, and the bound on slot production the floor of a new address
// rests on, measured on the chain. About ten calls; nothing is signed or broadcast.

import (
	"context"
	"math/big"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/gagliardetto/solana-go"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
)

const (
	// mainnetDepositOwner is a production deposit address, created 2026-10-28 18:31:59
	// UTC, paid 0.01 SOL by mainnetDeposit at slot mainnetDepositSlot two minutes later.
	mainnetDepositOwner   = "CS4tXnLdeN6bKhvbXR3e4RCVpCohxPyMZFuFjUjib9hT"
	mainnetDepositCreated = "2026-10-28T18:31:59Z"
	mainnetDeposit        = "48FkpKxQaWthJANqoL46waZfCWje5uUnWvg4ScHyu6GC4ADh4Veo89NC3zec2T5nJCjswW5emp3NNu58aby7Gh48"
	mainnetDepositSlot    = 462579272
)

// mainnetTokens is the catalog's mainnet list (chaincatalog, primary_tokens.go).
var mainnetTokens = []types.Token{
	{Symbol: models.SymbolUSDC, Contract: models.USDCMintSOL, Decimals: 6, ChainID: models.ChainSOL},
	{Symbol: models.SymbolUSDT, Contract: models.USDTMintSOL, Decimals: 6, ChainID: models.ChainSOL},
	{Symbol: "WSOL", Contract: models.WrappedSOLMint, Decimals: 9, ChainID: models.ChainSOL},
	{Symbol: "BONK", Contract: "DezXAZ8z7PnrnRJjz3wXBoRgixCa6xjnB7YaB1pPB263", Decimals: 5, ChainID: models.ChainSOL},
	{Symbol: "JUP", Contract: "JUPyiwrYJFskUPiHa7hkeR8VUtAeFoSYbKedZNsDvCN", Decimals: 6, ChainID: models.ChainSOL},
	{Symbol: "WIF", Contract: "EKpQGSJtjMFqKZ9KQanSqYXRcF8fBopzLHYxdM65zcjm", Decimals: 6, ChainID: models.ChainSOL},
}

func mainnetLive(t *testing.T) *SolanaLive {
	t.Helper()
	url := os.Getenv("SOLANA_MAINNET_LIVE_RPC_URL")
	if url == "" {
		url = "https://api.mainnet-beta.solana.com"
	}
	return NewSolanaLive(SolanaConfig{ChainIDStr: models.ChainSOL, NativeSymbol: models.NativeSOL, RPCURL: url, Tokens: mainnetTokens})
}

func TestLive_MainnetAccountsOfADepositAddressCoverEveryRegisteredToken(t *testing.T) {
	live := mainnetLive(t)
	listed, err := live.SignatureAccounts(context.Background(), mainnetDepositOwner)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.SkippedMints) > 0 {
		t.Fatalf("mainnet mints skipped: %v", listed.SkippedMints)
	}
	owner := solana.MustPublicKeyFromBase58(mainnetDepositOwner)
	want := []string{mainnetDepositOwner}
	for _, token := range mainnetTokens {
		// Every catalog mint is a Token-program mint on mainnet.
		ata, _, err := solana.FindAssociatedTokenAddress(owner, solana.MustPublicKeyFromBase58(token.Contract))
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, ata.String())
	}
	for _, account := range want {
		if !slices.Contains(listed.Accounts, account) {
			t.Errorf("accounts %v: %s missing", listed.Accounts, account)
		}
	}
	t.Logf("%d accounts read for the address: itself and its account of each of %d tokens", len(listed.Accounts), len(mainnetTokens))
}

func TestLive_MainnetDepositIsReadAtTheBlockScansPosition(t *testing.T) {
	live := mainnetLive(t)
	read, err := live.TransactionTransfers(context.Background(), mainnetDeposit)
	if err != nil {
		t.Fatal(err)
	}
	if read.Slot != mainnetDepositSlot {
		t.Fatalf("slot %d, want %d", read.Slot, mainnetDepositSlot)
	}
	var credit *types.DetectedTransfer
	for i := range read.Transfers {
		if read.Transfers[i].To == mainnetDepositOwner {
			credit = &read.Transfers[i]
		}
	}
	if credit == nil {
		t.Fatalf("no credit to %s among %d", mainnetDepositOwner, len(read.Transfers))
	}
	if credit.Asset != models.NativeSOL || credit.Amount.Cmp(big.NewInt(10_000_000)) != 0 || credit.Token != nil {
		t.Fatalf("credit %s %s, want 10000000 lamports of SOL", credit.Amount, credit.Asset)
	}
	t.Logf("0.01 SOL credited to %s at position %d", mainnetDepositOwner, credit.LogIndex)
}

// The floor of a new address is the head less SlotsWithin of its age: it must stay
// under every slot produced since it was created, which holds only while the chain
// never produces slots faster than the bound. Measured on the last 9000 slots, where
// the bound must keep at least twice the measured rate (it fails before slots get
// fast enough to reach it), and on the production deposit, whose slot must stay above
// the floor its address gets now.
func TestLive_MainnetSlotsNeverOutpaceTheBoundTheFloorOfANewAddressRestsOn(t *testing.T) {
	live := mainnetLive(t)
	ctx := context.Background()
	head, err := live.GetLatestBlock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	headTime, err := mainnetBlockTime(ctx, live, head)
	if err != nil {
		t.Fatal(err)
	}
	const span = 9000
	pastTime, err := mainnetBlockTime(ctx, live, head-span)
	if err != nil {
		t.Skipf("block time of slot %d not read (skipped slot or no history): %v", head-span, err)
	}
	elapsed := headTime.Sub(pastTime)
	if bound := live.SlotsWithin(elapsed); bound < 2*span {
		t.Fatalf("%d slots in %s: the bound of %d keeps less than twice the measured rate", span, elapsed, bound)
	}
	t.Logf("%d slots in %s (%s a slot), bound %d", span, elapsed, elapsed/span, live.SlotsWithin(elapsed))

	created, err := time.Parse(time.RFC3339, mainnetDepositCreated)
	if err != nil {
		t.Fatal(err)
	}
	if floor := head - live.SlotsWithin(time.Since(created)); floor > mainnetDepositSlot {
		t.Fatalf("floor %d of the address created %s is above its deposit at %d", floor, mainnetDepositCreated, mainnetDepositSlot)
	}
}

func mainnetBlockTime(ctx context.Context, live *SolanaLive, slot uint64) (time.Time, error) {
	var unix int64
	if err := live.rpc.Call(ctx, "getBlockTime", &unix, slot); err != nil {
		return time.Time{}, err
	}
	return time.Unix(unix, 0), nil
}
