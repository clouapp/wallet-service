//go:build solana_signatures_live

package solana

// Read-only check against the public devnet endpoint (go test -tags
// solana_signatures_live -run TestLive_ ./app/adapters/chain/solana/; SOLANA_LIVE_RPC_URL
// points it at another devnet endpoint): the accounts of the devnet trader HaomqG…,
// the newest page of its history, a listing above one of its signatures (which the
// endpoint must confirm reached it), and, for its newest successful transactions, the
// credits the signature sync reads compared with those ScanBlock reads in their
// slot, positions included. About a dozen calls; nothing is signed or broadcast.

import (
	"context"
	"os"
	"slices"
	"testing"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

func TestLive_DevnetSignaturesReadTheCreditsTheBlockScanReads(t *testing.T) {
	url := os.Getenv("SOLANA_LIVE_RPC_URL")
	if url == "" {
		url = "https://api.devnet.solana.com"
	}
	live := NewSolanaLive(SolanaConfig{ChainIDStr: models.ChainSOL, NativeSymbol: models.NativeSOL, RPCURL: url,
		Tokens: []types.Token{devnetUSDC, devnetWSOL, token2022Token}})
	ctx := context.Background()

	accounts, err := live.SignatureAccounts(ctx, ataFixtureOwner)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{ataFixtureOwner, ataFixtureUSDC, ataFixtureToken2022} {
		if !slices.Contains(accounts.Accounts, want) {
			t.Fatalf("accounts %v, want %s among them", accounts.Accounts, want)
		}
	}

	history, err := live.AccountSignatures(ctx, ataFixtureOwner, chain.SignatureQuery{MaxPages: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Signatures) == 0 {
		t.Skip("the trader has no history on this endpoint")
	}
	if history.Head < history.Signatures[0].Slot {
		t.Fatalf("head %d below the newest signature's slot %d", history.Head, history.Signatures[0].Slot)
	}
	if len(history.Signatures) > 3 {
		cursor := history.Signatures[3].Signature
		above, err := live.AccountSignatures(ctx, ataFixtureOwner, chain.SignatureQuery{Until: cursor, MaxPages: 1})
		if err != nil {
			t.Fatal(err)
		}
		if !above.Ended || len(above.Signatures) < 3 || above.Signatures[len(above.Signatures)-1].Signature != history.Signatures[2].Signature {
			t.Fatalf("above %s: %d signatures, ended %v: want the endpoint to confirm the cursor right below the three newer ones", cursor, len(above.Signatures), above.Ended)
		}
		t.Logf("the endpoint confirmed the cursor %s below %d newer signatures", cursor, len(above.Signatures))
	}
	compared := 0
	for i := 0; i < len(history.Signatures) && compared < 2; i++ {
		signature := history.Signatures[i]
		if signature.Failed {
			continue
		}
		read, err := live.TransactionTransfers(ctx, signature.Signature)
		if err != nil {
			t.Fatal(err)
		}
		if read.Slot != signature.Slot {
			t.Fatalf("%s: transaction slot %d, history slot %d", signature.Signature, read.Slot, signature.Slot)
		}
		scanned, err := live.ScanBlock(ctx, read.Slot)
		if err != nil {
			t.Fatal(err)
		}
		want := transfersOf(scanned, signature.Signature)
		if len(want) != len(read.Transfers) {
			t.Fatalf("%s: %d credits by signature, %d by block", signature.Signature, len(read.Transfers), len(want))
		}
		for j := range want {
			want[j].BlockHash = ""
			got := read.Transfers[j]
			if got.LogIndex != want[j].LogIndex || got.To != want[j].To || got.From != want[j].From || got.Amount.Cmp(want[j].Amount) != 0 || got.Asset != want[j].Asset {
				t.Fatalf("%s credit %d: by signature %+v, by block %+v", signature.Signature, j, got, want[j])
			}
		}
		t.Logf("%s (slot %d): %d credits at the same positions", signature.Signature, read.Slot, len(want))
		compared++
	}
}
