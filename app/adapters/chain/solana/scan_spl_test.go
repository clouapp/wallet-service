package solana

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
)

// Devnet slot 512232469 (recorded 2026-10-18) holds legacy, version-0 and version-1
// transactions. Its version-1 swap 2kb1Kr… moves devnet USDC and wrapped SOL from
// vH9xX8… to 5uDDz3wN… and credits lamports to three accounts.
const (
	v1FixtureSlot      = 512232469
	v1FixtureBlockhash = "3VMvKFUd8KCcYRcwjmBMftHNBaerDMoSZHmHhuiPtCin"
	v1FixtureBlockTime = 1792278020
	v1FixtureSwap      = "2kb1KrYNCc82UoDgAR742bysmxtYXrwBbgbTUVEPmQMb3udTR2ykDod6G7isQhp8fReVd2sWp3xmkr4t1A2bonsk"
	v1FixtureFeePayer  = "ZwXpuLPxBkuwUdqSFJrXstPJ1nXBTzwLaceu653ZDDP"
	v1FixtureSeller    = "vH9xX8SkhoNirTUw9UKsRiN78hQxmEVz3azsYpXbN7U"
	v1FixtureBuyer     = "5uDDz3wNUz8YDafuEhS2ZELzsU6529dM9GS4LJrfNeoi"

	// Devnet slot 512232636 (recorded 2026-10-18): legacy swap 3mbCjk… where
	// HaomqG… pays devnet USDC to two owners and receives a Token-2022 mint.
	twoOwnersFixtureSlot      = 512232636
	twoOwnersFixtureBlockhash = "4C45wUfKto1ee19V7dUCn2pdqb5TDMpXxDeku7PYNBaM"
	twoOwnersFixtureSwap      = "3mbCjkwadgwqfftWmnHzEmMpyH4mUPPuhxywVLbcM9kVgAyVkjXwdBWtiM2wjjRzTVXimbkNAWeKTT1LBwJ11vBk"
	twoOwnersFixtureTrader    = "HaomqGZ9epXsBwmcpGKrJAy2kg8CiCPrWtbCWpFymyfp"
	twoOwnersFixturePool      = "5G4VdPp71bGYT9ugjMRU9nCkhgcbdSfCbPC1GeJw2VMk"
	twoOwnersFixtureFeeOwner  = "5YxQFdt3Tr9zJLvkFccqXVUwhdTWJQc1fFg2YPbxvxeD"
	token2022FixtureMint      = "3sji3U1wsWpADxi2x9VEYysjjh2L2ejmybPPH8PTEcZ5"

	// Circle's devnet USDC mint and the native (wrapped SOL) mint.
	devnetUSDCMint = "4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU"
	nativeSOLMint  = "So11111111111111111111111111111111111111112"
)

var (
	devnetUSDC = types.Token{Symbol: "USDC", Name: "USD Coin", Contract: devnetUSDCMint, Decimals: 6, ChainID: models.ChainSOL}
	devnetWSOL = types.Token{Symbol: "WSOL", Name: "Wrapped SOL", Contract: nativeSOLMint, Decimals: 9, ChainID: models.ChainSOL}
)

// serveVersionedSolanaFixture is a devnet node that answers the recorded
// "Transaction version (1) is not supported" error to a request whose
// maxSupportedTransactionVersion is missing or below 1, as devnet does for a block or
// transaction that holds a version-1 transaction, and the recorded answer otherwise.
func serveVersionedSolanaFixture(t *testing.T, fixture string, tokens ...types.Token) (*SolanaLive, *[]recordedRPCRequest) {
	t.Helper()
	answer := readSolanaFixture(t, fixture)
	rejection := readSolanaFixture(t, "getBlock_v1_unsupported.json")
	var requests []recordedRPCRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		var req recordedRPCRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Error(err)
			return
		}
		requests = append(requests, req)
		w.Header().Set("Content-Type", "application/json")
		if requestedTransactionVersion(req) < 1 {
			_, _ = w.Write(rejection)
			return
		}
		_, _ = w.Write(answer)
	}))
	t.Cleanup(srv.Close)
	live := NewSolanaLive(SolanaConfig{ChainIDStr: models.ChainSOL, NativeSymbol: models.NativeSOL, RPCURL: srv.URL, Tokens: tokens})
	return live, &requests
}

// readSolanaFixture reads a recorded answer from testdata/solana, and nothing outside it.
func readSolanaFixture(t *testing.T, fixture string) []byte {
	t.Helper()
	body, err := fs.ReadFile(os.DirFS(filepath.Join("testdata", "solana")), fixture)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// requestedTransactionVersion is the maxSupportedTransactionVersion of the request's
// options object, or -1 when it carries none.
func requestedTransactionVersion(req recordedRPCRequest) float64 {
	for _, param := range req.Params {
		var opts map[string]interface{}
		if json.Unmarshal(param, &opts) != nil {
			continue
		}
		if version, ok := opts["maxSupportedTransactionVersion"].(float64); ok {
			return version
		}
	}
	return -1
}

type wantTransfer struct {
	tx, from, to string
	amount       int64
	asset        string
	contract     string
	index        int
}

func assertTransfers(t *testing.T, got []types.DetectedTransfer, want []wantTransfer, slot uint64, blockhash string, blockTime int64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("want %d transfers, got %d: %+v", len(want), len(got), got)
	}
	for i, w := range want {
		transfer := got[i]
		if transfer.TxHash != w.tx || transfer.From != w.from || transfer.To != w.to || transfer.Amount.Int64() != w.amount ||
			transfer.Asset != w.asset || transfer.LogIndex != w.index {
			t.Fatalf("transfer %d: got %+v (amount %s), want %+v", i, transfer, transfer.Amount, w)
		}
		switch {
		case w.contract == "" && transfer.Token != nil:
			t.Fatalf("transfer %d is native but carries token %+v", i, transfer.Token)
		case w.contract != "" && (transfer.Token == nil || transfer.Token.Contract != w.contract || transfer.Token.Symbol != w.asset):
			t.Fatalf("transfer %d token %+v, want %s %s", i, transfer.Token, w.asset, w.contract)
		}
		if transfer.BlockNumber != slot || transfer.BlockHash != blockhash || !transfer.Timestamp.Equal(time.Unix(blockTime, 0)) {
			t.Fatalf("transfer %d block fields: %+v", i, transfer)
		}
	}
}

// The three lamport credits of the version-1 swap; the fee payer's change is a fee.
var v1FixtureNativeCredits = []wantTransfer{
	{v1FixtureSwap, v1FixtureFeePayer, "8uBKcNhfn3DoaLU3ycmaunkEJ5zPU14masYoYE14KZ7", 5_000, models.NativeSOL, "", 1},
	{v1FixtureSwap, v1FixtureFeePayer, "5jt5jvW22vR6Hij4NWbJL9mZZzL3mgeGzcQhSdFECF4E", 6_010, models.NativeSOL, "", 4},
	{v1FixtureSwap, v1FixtureFeePayer, "955q2iVFTaJZR39F8j7QwqFZiVf5U9wFUfbDNWUenGqb", 1_488_440, models.NativeSOL, "", 6},
}

func TestSolana_ScanBlock_ReadsABlockWithVersionOneTransactions(t *testing.T) {
	live, requests := serveVersionedSolanaFixture(t, "getBlock_v1_spl.json")

	transfers, err := live.ScanBlock(context.Background(), v1FixtureSlot)
	if err != nil {
		t.Fatalf("a block holding version-1 transactions must be read: %v", err)
	}
	assertTransfers(t, transfers, v1FixtureNativeCredits, v1FixtureSlot, v1FixtureBlockhash, v1FixtureBlockTime)
	if len(*requests) != 1 || (*requests)[0].Method != "getBlock" || requestedTransactionVersion((*requests)[0]) != solanaMaxSupportedTxVersion {
		t.Fatalf("requests %+v", *requests)
	}
}

func TestSolana_TransactionFee_ReadsAVersionOneTransaction(t *testing.T) {
	live, requests := serveVersionedSolanaFixture(t, "getTransaction_v1.json")

	fee, err := live.TransactionFee(context.Background(), v1FixtureSwap)
	if err != nil {
		t.Fatalf("the fee of a version-1 transaction must be read: %v", err)
	}
	if fee.Int64() != 5001 {
		t.Fatalf("fee %s, want the recorded meta.fee 5001", fee)
	}
	if len(*requests) != 1 || (*requests)[0].Method != "getTransaction" || requestedTransactionVersion((*requests)[0]) != solanaMaxSupportedTxVersion {
		t.Fatalf("requests %+v", *requests)
	}
}

func TestSolana_ScanBlock_CreditsRegisteredSPLTokensToTheTokenAccountOwner(t *testing.T) {
	live, _ := serveVersionedSolanaFixture(t, "getBlock_v1_spl.json", devnetUSDC, devnetWSOL)

	transfers, err := live.ScanBlock(context.Background(), v1FixtureSlot)
	if err != nil {
		t.Fatal(err)
	}
	// Token credits are numbered after every possible account index (256), by the
	// index of the credited token account (3 for USDC, 4 for wrapped SOL), so they
	// never collide with the lamport credits of the same transaction.
	want := append(append([]wantTransfer(nil), v1FixtureNativeCredits...),
		wantTransfer{v1FixtureSwap, v1FixtureSeller, v1FixtureBuyer, 2_200, "USDC", devnetUSDCMint, 259},
		wantTransfer{v1FixtureSwap, v1FixtureSeller, v1FixtureBuyer, 6_010, "WSOL", nativeSOLMint, 260},
	)
	assertTransfers(t, transfers, want, v1FixtureSlot, v1FixtureBlockhash, v1FixtureBlockTime)
	if transfers[3].Token.Decimals != 6 || transfers[4].Token.Decimals != 9 {
		t.Fatalf("token decimals come from the registry rows, got %+v and %+v", transfers[3].Token, transfers[4].Token)
	}
}

func TestSolana_ScanBlock_CreditsEachOwnerOfAMintAndToken2022(t *testing.T) {
	token2022 := types.Token{Symbol: "T22", Contract: token2022FixtureMint, Decimals: 6, ChainID: models.ChainSOL}
	live, _ := serveVersionedSolanaFixture(t, "getBlock_spl_two_owners.json", devnetUSDC, token2022)

	transfers, err := live.ScanBlock(context.Background(), twoOwnersFixtureSlot)
	if err != nil {
		t.Fatal(err)
	}
	var tokenCredits []types.DetectedTransfer
	for _, transfer := range transfers {
		if transfer.Token != nil {
			tokenCredits = append(tokenCredits, transfer)
		}
	}
	// The Token-2022 account is index 3, the USDC accounts 6 and 8: after 256.
	assertTransfers(t, tokenCredits, []wantTransfer{
		{twoOwnersFixtureSwap, twoOwnersFixturePool, twoOwnersFixtureTrader, 1_252_097_624_717, "T22", token2022FixtureMint, 259},
		{twoOwnersFixtureSwap, twoOwnersFixtureTrader, twoOwnersFixturePool, 36_868, "USDC", devnetUSDCMint, 262},
		{twoOwnersFixtureSwap, twoOwnersFixtureTrader, twoOwnersFixtureFeeOwner, 1, "USDC", devnetUSDCMint, 264},
	}, twoOwnersFixtureSlot, twoOwnersFixtureBlockhash, 1792278060)
}

func TestSolana_ScanBlock_IgnoresUnregisteredMintsAndTokensOfOtherChains(t *testing.T) {
	otherChain := devnetUSDC
	otherChain.ChainID = models.ChainTSOL
	live, _ := serveVersionedSolanaFixture(t, "getBlock_v1_spl.json", otherChain)

	transfers, err := live.ScanBlock(context.Background(), v1FixtureSlot)
	if err != nil {
		t.Fatal(err)
	}
	assertTransfers(t, transfers, v1FixtureNativeCredits, v1FixtureSlot, v1FixtureBlockhash, v1FixtureBlockTime)
}

func TestSolana_ScanBlock_AnInvalidRegisteredMintFailsTheScan(t *testing.T) {
	broken := devnetUSDC
	broken.Contract = "not-a-mint"
	live, requests := serveVersionedSolanaFixture(t, "getBlock_v1_spl.json", broken)

	if _, err := live.ScanBlock(context.Background(), v1FixtureSlot); err == nil {
		t.Fatal("a registry mint that is not a public key must fail the scan, not hide its deposits")
	}
	if len(*requests) != 0 {
		t.Fatalf("the block must not be fetched, got %+v", *requests)
	}
}

// tokenBlock is one successful transaction with the given account keys and token
// balances; lamport balances do not move.
func tokenBlock(keys []string, pre, post []solanaTokenBalance, failed bool) *solanaBlockAccounts {
	tx := solanaBlockTransaction{Meta: &solanaTransactionMeta{
		PreBalances:       make([]uint64, len(keys)),
		PostBalances:      make([]uint64, len(keys)),
		PreTokenBalances:  pre,
		PostTokenBalances: post,
	}}
	if failed {
		tx.Meta.Err = map[string]interface{}{"InstructionError": []interface{}{0, "Custom"}}
	}
	tx.Transaction.Signatures = []string{"sig"}
	for i, key := range keys {
		tx.Transaction.AccountKeys = append(tx.Transaction.AccountKeys, solanaBlockAccountKey{Pubkey: key, Signer: i == 0})
	}
	return &solanaBlockAccounts{Blockhash: "hash", Transactions: []solanaBlockTransaction{tx}}
}

func usdcBalance(index int, owner, amount string) solanaTokenBalance {
	return solanaTokenBalance{AccountIndex: index, Mint: devnetUSDCMint, Owner: owner, UITokenAmount: solanaTokenAmount{Amount: amount, Decimals: 6}}
}

func usdcIndex() map[string]types.Token {
	return map[string]types.Token{devnetUSDCMint: devnetUSDC}
}

func TestSolana_BlockCredits_SkipFailedTransactionsAndOwnersWhoseTotalDidNotRise(t *testing.T) {
	keys := []string{"payer", "ataA", "ataB", "ataC"}
	// The owner moves 5 USDC between two of its own token accounts: no deposit.
	selfMove := tokenBlock(keys,
		[]solanaTokenBalance{usdcBalance(1, "owner", "10"), usdcBalance(2, "owner", "0")},
		[]solanaTokenBalance{usdcBalance(1, "owner", "5"), usdcBalance(2, "owner", "5")}, false)
	if got, err := solanaBlockCredits(1, selfMove, models.NativeSOL, usdcIndex()); err != nil || len(got) != 0 {
		t.Fatalf("a move between one owner's accounts is not a deposit, got %+v, %v", got, err)
	}

	failed := tokenBlock(keys, nil, []solanaTokenBalance{usdcBalance(3, "owner", "7")}, true)
	if got, err := solanaBlockCredits(1, failed, models.NativeSOL, usdcIndex()); err != nil || len(got) != 0 {
		t.Fatalf("a failed transaction moves no tokens, got %+v, %v", got, err)
	}

	// A token account created by the transaction has no pre balance; nobody's balance
	// fell (a mint), so the fee payer is the sender.
	minted := tokenBlock(keys, nil, []solanaTokenBalance{usdcBalance(3, "owner", "7")}, false)
	got, err := solanaBlockCredits(1, minted, models.NativeSOL, usdcIndex())
	if err != nil || len(got) != 1 || got[0].To != "owner" || got[0].From != "payer" || got[0].Amount.Int64() != 7 || got[0].LogIndex != 259 {
		t.Fatalf("minted credit %+v, %v", got, err)
	}
}

func TestSolana_BlockCredits_AMalformedRegisteredBalanceFailsTheBlock(t *testing.T) {
	keys := []string{"payer", "ata"}
	cases := map[string]solanaTokenBalance{
		"unparsable amount":   usdcBalance(1, "owner", "1.5"),
		"negative amount":     usdcBalance(1, "owner", "-1"),
		"missing owner":       usdcBalance(1, "", "3"),
		"index past the keys": usdcBalance(2, "owner", "3"),
	}
	for name, balance := range cases {
		t.Run(name, func(t *testing.T) {
			block := tokenBlock(keys, nil, []solanaTokenBalance{balance}, false)
			if _, err := solanaBlockCredits(1, block, models.NativeSOL, usdcIndex()); err == nil {
				t.Fatal("a registered token balance that cannot be read must fail the block so it is retried")
			}
		})
	}
	unregistered := usdcBalance(1, "", "1.5")
	unregistered.Mint = nativeSOLMint
	block := tokenBlock(keys, nil, []solanaTokenBalance{unregistered}, false)
	if got, err := solanaBlockCredits(1, block, models.NativeSOL, usdcIndex()); err != nil || len(got) != 0 {
		t.Fatalf("an unregistered mint is not read, got %+v, %v", got, err)
	}
}
