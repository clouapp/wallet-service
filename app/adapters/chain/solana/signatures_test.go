package solana

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gagliardetto/solana-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

// Devnet slot 514235140 (recorded 2026-10-23): the version-0 Raydium CLMM swap
// 2yMCe5… loads 15 writable and 15 readonly keys from address lookup tables, and
// its lamport and token credits are on loaded keys (indexes 5 to 19).
const (
	lookupFixtureSlot      = 514235140
	lookupFixtureBlockhash = "FCkXq6oHeDCPz5casNXyAnseVax3d2XDjMcXbNXqGeEY"
	lookupFixtureBlockTime = 1792755839
	lookupFixtureSwap      = "2yMCe5i8u3jpxnMr18chCCZxP3JDDbj2WJD4SzbpTjb7643q9Enfidtx12eJK8pHuiN11eHTqn69C7FE9kSVpt2o"
	// 634Fzh… of slot 512232469 (getBlock_v1_spl.json) loads 4 writable and 1
	// readonly key and credits nothing.
	lookupFeeOnlySwap = "634FzhFYjUNj6VaDHWRAJM4aUm4HEPzct3KQJ9pZ8CfjUmd9zgFfqStZDTFnL66ySHgsf8nm7scm7T2SgNH9jtgs"

	// The mints the CLMM swap moves.
	lookupMintA = "FyLh5im2jmrfcevhEj55hXn1vuCm7ar39tLdfFrmFWRF"
	lookupMintB = "B3xAZogahaSexqDtpKihFd8MnLV5fAkBbx75EtPtKPai"

	// HaomqG… holds, on devnet, the Token-2022 account 69w14Z… of token2022FixtureMint
	// and the Token account J97xc3… of devnet USDC (getBlock_spl_two_owners.json): both
	// are its associated token accounts, under each mint's own program.
	ataFixtureOwner        = "HaomqGZ9epXsBwmcpGKrJAy2kg8CiCPrWtbCWpFymyfp"
	ataFixtureUSDC         = "J97xc3FEjkZ9jXRAUjXaqK1r4j16u6rpYzsV8YA4HGaj"
	ataFixtureToken2022    = "69w14ZjEJwXqwe7j8igT6R1GtMZttwaQ8GCoHqaKCRyj"
	ataFixtureOtherAccount = "GsVBKjffkB769p9tHTZWoAX3r9T6dXoDTJr3f7XutJH7"
	unregisteredMint       = "So11111111111111111111111111111111111111112"
)

var (
	lookupTokens = []types.Token{
		devnetWSOL,
		{Symbol: "TKA", Contract: lookupMintA, Decimals: 9, ChainID: models.ChainSOL},
		{Symbol: "TKB", Contract: lookupMintB, Decimals: 6, ChainID: models.ChainSOL},
	}
	token2022Token = types.Token{Symbol: "T22", Contract: token2022FixtureMint, Decimals: 6, ChainID: models.ChainSOL}
)

// solanaRequest is one JSON-RPC request a fake node received.
type solanaRequest struct {
	Method string            `json:"method"`
	Params []json.RawMessage `json:"params"`
}

func (r solanaRequest) param(t *testing.T, i int, out any) {
	t.Helper()
	require.Greater(t, len(r.Params), i, "request %s has no parameter %d", r.Method, i)
	require.NoError(t, json.Unmarshal(r.Params[i], out))
}

// methodNode is a fake Solana node: answer returns the JSON-RPC body after "id" for
// one request ("result":... or "error":...), or "" for an HTTP 503.
type methodNode struct {
	server   *httptest.Server
	mu       sync.Mutex
	requests []solanaRequest
}

func newMethodNode(t *testing.T, answer func(req solanaRequest) string) *methodNode {
	t.Helper()
	node := &methodNode{}
	node.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		var req solanaRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Error(err)
			return
		}
		node.mu.Lock()
		node.requests = append(node.requests, req)
		node.mu.Unlock()
		body := answer(req)
		if body == "" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,`+body+`}`)
	}))
	t.Cleanup(node.server.Close)
	return node
}

func (n *methodNode) calls(method string) []solanaRequest {
	n.mu.Lock()
	defer n.mu.Unlock()
	var calls []solanaRequest
	for _, req := range n.requests {
		if req.Method == method {
			calls = append(calls, req)
		}
	}
	return calls
}

func liveOn(url string, tokens ...types.Token) *SolanaLive {
	return NewSolanaLive(SolanaConfig{ChainIDStr: models.ChainSOL, NativeSymbol: models.NativeSOL, RPCURL: url, Tokens: tokens})
}

// fixtureResult is the "result" member of a recorded answer.
func fixtureResult(t *testing.T, fixture string) string {
	t.Helper()
	var answer struct {
		Result json.RawMessage `json:"result"`
	}
	require.NoError(t, json.Unmarshal(readSolanaFixture(t, fixture), &answer))
	return `"result":` + string(answer.Result)
}

// accountHistory is the newest-first signature history of one account as a node
// keeps it: count signatures, two per slot, the newest at slot top.
type accountHistory struct {
	signatures []solanaSignatureInfo
}

func newAccountHistory(count int, top uint64) *accountHistory {
	history := &accountHistory{}
	for i := 0; i < count; i++ {
		history.signatures = append(history.signatures, solanaSignatureInfo{Signature: fmt.Sprintf("sig-%05d", count-1-i), Slot: top - uint64(i/2)})
	}
	return history
}

// page answers getSignaturesForAddress as a node does: newest first, starting after
// "before" and stopping at "until" (both left out), at most "limit" entries.
func (h *accountHistory) page(t *testing.T, req solanaRequest) string {
	t.Helper()
	var options struct {
		Before, Until, Commitment string
		Limit                     int
	}
	req.param(t, 1, &options)
	if options.Commitment != solanaCommitmentFinalized || options.Limit != solanaSignaturePageLimit {
		t.Errorf("getSignaturesForAddress options %+v, want finalized and %d per page", options, solanaSignaturePageLimit)
	}
	start := 0
	if options.Before != "" {
		start = h.index(options.Before) + 1
	}
	var page []solanaSignatureInfo
	for i := start; i < len(h.signatures) && len(page) < options.Limit; i++ {
		if h.signatures[i].Signature == options.Until {
			break
		}
		page = append(page, h.signatures[i])
	}
	body, err := json.Marshal(page)
	require.NoError(t, err)
	return `"result":` + string(body)
}

func (h *accountHistory) index(signature string) int {
	for i, entry := range h.signatures {
		if entry.Signature == signature {
			return i
		}
	}
	return len(h.signatures)
}

func historyNode(t *testing.T, history *accountHistory) *methodNode {
	return newMethodNode(t, func(req solanaRequest) string {
		if req.Method != "getSignaturesForAddress" {
			return `"error":{"code":-32601,"message":"Method not found"}`
		}
		return history.page(t, req)
	})
}

func TestSolana_AccountSignatures_PagesBackUntilTheCursorAndReturnsThemOldestFirst(t *testing.T) {
	history := newAccountHistory(2600, 9000)
	node := historyNode(t, history)
	// sig-00399 is the newest signature already processed: the 2200 newer ones are new.
	until := "sig-00399"

	got, err := liveOn(node.server.URL).AccountSignatures(context.Background(), ataFixtureOwner, chain.SignatureQuery{Until: until, MaxPages: 10})

	require.NoError(t, err)
	require.Len(t, got.Signatures, 2200)
	assert.False(t, got.Truncated)
	assert.Equal(t, "sig-00400", got.Signatures[0].Signature, "oldest first, the cursor left out")
	assert.Equal(t, "sig-02599", got.Signatures[2199].Signature)
	for i := 1; i < len(got.Signatures); i++ {
		require.LessOrEqual(t, got.Signatures[i-1].Slot, got.Signatures[i].Slot, "slots never go back in the returned order")
	}
	pages := node.calls("getSignaturesForAddress")
	require.Len(t, pages, 3, "1000 + 1000 + 200: the short page ends the history")
	for i, page := range pages {
		var account string
		page.param(t, 0, &account)
		var options struct{ Before, Until string }
		page.param(t, 1, &options)
		assert.Equal(t, ataFixtureOwner, account)
		assert.Equal(t, until, options.Until, "page %d keeps the cursor", i)
		wantBefore := ""
		if i > 0 {
			wantBefore = fmt.Sprintf("sig-%05d", 2600-1000*i)
		}
		assert.Equal(t, wantBefore, options.Before, "page %d starts before the last signature of the page before it", i)
	}
}

func TestSolana_AccountSignatures_StopsAtTheFirstSignatureBelowTheFloor(t *testing.T) {
	history := newAccountHistory(3000, 9000)
	node := historyNode(t, history)
	// Two signatures per slot from 9000 down: slot 8250 holds sig-01499 and sig-01498,
	// on the second page; slot 8249 is below the floor.
	got, err := liveOn(node.server.URL).AccountSignatures(context.Background(), ataFixtureOwner,
		chain.SignatureQuery{FloorSlot: 8250, HasFloor: true, MaxPages: 10})

	require.NoError(t, err)
	assert.False(t, got.Truncated)
	require.Len(t, got.Signatures, 1502, "slots 8250 to 9000, the floor slot included")
	assert.Equal(t, uint64(8250), got.Signatures[0].Slot)
	assert.Len(t, node.calls("getSignaturesForAddress"), 2, "no page is read past the floor")
}

func TestSolana_AccountSignatures_CutsAHistoryLongerThanMaxPages(t *testing.T) {
	history := newAccountHistory(2500, 9000)
	node := historyNode(t, history)

	got, err := liveOn(node.server.URL).AccountSignatures(context.Background(), ataFixtureOwner, chain.SignatureQuery{MaxPages: 2})

	require.NoError(t, err)
	assert.True(t, got.Truncated, "500 older signatures were not read")
	require.Len(t, got.Signatures, 2000)
	assert.Equal(t, "sig-00500", got.Signatures[0].Signature, "the newest pages are kept")
	assert.Len(t, node.calls("getSignaturesForAddress"), 2)
}

func TestSolana_AccountSignatures_ReadsTheRecordedPageAndMarksFailedTransactions(t *testing.T) {
	var page []map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(fixtureResult(t, "getSignaturesForAddress_page.json"), `"result":`)), &page))
	require.Len(t, page, 10)
	page[3]["err"] = map[string]any{"InstructionError": []any{0, map[string]any{"Custom": 6022}}}
	body, err := json.Marshal(page)
	require.NoError(t, err)
	node := newMethodNode(t, func(solanaRequest) string { return `"result":` + string(body) })

	got, err := liveOn(node.server.URL).AccountSignatures(context.Background(), ataFixtureOwner, chain.SignatureQuery{MaxPages: 1})

	require.NoError(t, err)
	assert.False(t, got.Truncated, "a short page is the whole history")
	require.Len(t, got.Signatures, 10)
	newest := got.Signatures[9]
	assert.Equal(t, "2yMCe5i8u3jpxnMr18chCCZxP3JDDbj2WJD4SzbpTjb7643q9Enfidtx12eJK8pHuiN11eHTqn69C7FE9kSVpt2o", newest.Signature)
	assert.Equal(t, uint64(514235140), newest.Slot)
	for i, signature := range got.Signatures {
		assert.Equal(t, i == 6, signature.Failed, "only the entry with an err is a failed transaction (signature %d)", i)
	}
}

func TestSolana_AccountSignatures_RefusesAMalformedQueryOrAnswer(t *testing.T) {
	node := newMethodNode(t, func(solanaRequest) string { return `"result":[{"signature":"","slot":5,"err":null}]` })
	live := liveOn(node.server.URL)

	_, err := live.AccountSignatures(context.Background(), "not a key", chain.SignatureQuery{MaxPages: 1})
	assert.ErrorIs(t, err, chain.ErrInvalidAddress)
	_, err = live.AccountSignatures(context.Background(), ataFixtureOwner, chain.SignatureQuery{})
	assert.Error(t, err, "a query that allows no page reads nothing")
	_, err = live.AccountSignatures(context.Background(), ataFixtureOwner, chain.SignatureQuery{MaxPages: 1})
	assert.Error(t, err, "an entry without a signature is an error")
}

// A node that does not know the signature a page starts before answers an empty
// page, which reads like the end of the history: every page of one account comes
// from the endpoint that answered the first one, and its failure fails the history.
func TestSolana_AccountSignatures_ReadsEveryPageOfAnAccountFromOneEndpoint(t *testing.T) {
	history := newAccountHistory(1500, 9000)
	var primaryPages int
	var mu sync.Mutex
	primary := newMethodNode(t, func(req solanaRequest) string {
		switch req.Method {
		case "getGenesisHash":
			return devnetGenesisResult
		case "getSignaturesForAddress":
			mu.Lock()
			defer mu.Unlock()
			primaryPages++
			if primaryPages > 1 {
				return ""
			}
			return history.page(t, req)
		}
		return `"error":{"code":-32601,"message":"Method not found"}`
	})
	fallback := newMethodNode(t, func(req solanaRequest) string {
		if req.Method == "getGenesisHash" {
			return devnetGenesisResult
		}
		return `"result":[]`
	})
	live := devnetWithFallbacks(primary.server.URL, fallback.server.URL)

	_, err := live.AccountSignatures(context.Background(), ataFixtureOwner, chain.SignatureQuery{MaxPages: 10})

	require.Error(t, err, "the second page failed on the endpoint that served the first")
	assert.Empty(t, fallback.calls("getSignaturesForAddress"), "no page comes from another endpoint")
}

// mintNode answers getAccountInfo for the recorded mints and getTokenAccountsByOwner
// with the token accounts of tokenAccounts (program → accounts), in the recorded shape:
// the mint is the account data sliced to its first 32 bytes, base64.
func mintNode(t *testing.T, tokenAccounts map[string][][2]string) *methodNode {
	t.Helper()
	owners := map[string]string{
		devnetUSDCMint:       fixtureResult(t, "getAccountInfo_mint_token.json"),
		token2022FixtureMint: fixtureResult(t, "getAccountInfo_mint_token2022.json"),
	}
	return newMethodNode(t, func(req solanaRequest) string {
		switch req.Method {
		case "getAccountInfo":
			var mint string
			req.param(t, 0, &mint)
			var options struct {
				Encoding, Commitment string
				DataSlice            struct{ Offset, Length int }
			}
			req.param(t, 1, &options)
			if options.Encoding != "base64" || options.Commitment != solanaCommitmentFinalized || options.DataSlice.Length != 0 {
				t.Errorf("getAccountInfo options %+v, want base64, finalized and no data", options)
			}
			if answer, ok := owners[mint]; ok {
				return answer
			}
			return `"result":{"context":{"slot":1},"value":null}`
		case "getTokenAccountsByOwner":
			var owner string
			req.param(t, 0, &owner)
			var filter struct {
				ProgramID string `json:"programId"`
			}
			req.param(t, 1, &filter)
			var options struct {
				Encoding  string
				DataSlice struct{ Offset, Length int }
			}
			req.param(t, 2, &options)
			if owner != ataFixtureOwner || options.Encoding != "base64" || options.DataSlice != (struct{ Offset, Length int }{0, solanaMintAddressLength}) {
				t.Errorf("getTokenAccountsByOwner %s options %+v", owner, options)
			}
			var value []string
			for _, held := range tokenAccounts[filter.ProgramID] {
				mint := solana.MustPublicKeyFromBase58(held[1])
				value = append(value, fmt.Sprintf(`{"account":{"data":["%s","base64"],"executable":false,"lamports":2039280,"owner":"%s","space":165},"pubkey":"%s"}`,
					base64.StdEncoding.EncodeToString(mint[:]), filter.ProgramID, held[0]))
			}
			return `"result":{"context":{"slot":1},"value":[` + strings.Join(value, ",") + `]}`
		}
		return `"error":{"code":-32601,"message":"Method not found"}`
	})
}

func TestSolana_SignatureAccounts_DerivesEachAssociatedAccountWithTheMintsProgramAndMergesTheHeldOnes(t *testing.T) {
	node := mintNode(t, map[string][][2]string{
		solana.TokenProgramID.String(): {
			{ataFixtureUSDC, devnetUSDCMint},                                   // the associated account, listed again
			{ataFixtureOtherAccount, devnetUSDCMint},                           // a token account that is not the associated one
			{"FiacRyvCaZsHF1JqV1pmFhvQNSTykDJyRjd9PGX3dSFE", unregisteredMint}, // a mint the registry does not list
		},
		solana.Token2022ProgramID.String(): {{ataFixtureToken2022, token2022FixtureMint}},
	})
	live := liveOn(node.server.URL, devnetUSDC, token2022Token)

	got, err := live.SignatureAccounts(context.Background(), ataFixtureOwner)

	require.NoError(t, err)
	assert.Equal(t, []string{ataFixtureOwner, ataFixtureToken2022, ataFixtureUSDC, ataFixtureOtherAccount}, got,
		"the owner, the associated accounts by mint, then the held accounts of registered mints, once each")
	tokenATA, _, err := solana.FindAssociatedTokenAddress(solana.MustPublicKeyFromBase58(ataFixtureOwner), solana.MustPublicKeyFromBase58(token2022FixtureMint))
	require.NoError(t, err)
	assert.NotEqual(t, tokenATA.String(), ataFixtureToken2022, "a Token-2022 account is not derived under the Token program")
	programs := node.calls("getTokenAccountsByOwner")
	require.Len(t, programs, 2, "one listing per token program in use")

	_, err = live.SignatureAccounts(context.Background(), ataFixtureOwner)
	require.NoError(t, err)
	assert.Len(t, node.calls("getAccountInfo"), 2, "each mint's program is read once, then cached")
}

func TestSolana_SignatureAccounts_ListsOnlyTheProgramsOfTheRegisteredMints(t *testing.T) {
	node := mintNode(t, map[string][][2]string{solana.TokenProgramID.String(): {{ataFixtureOtherAccount, devnetUSDCMint}}})

	got, err := liveOn(node.server.URL, devnetUSDC).SignatureAccounts(context.Background(), ataFixtureOwner)

	require.NoError(t, err)
	assert.Equal(t, []string{ataFixtureOwner, ataFixtureUSDC, ataFixtureOtherAccount}, got)
	calls := node.calls("getTokenAccountsByOwner")
	require.Len(t, calls, 1)
	var filter struct {
		ProgramID string `json:"programId"`
	}
	calls[0].param(t, 1, &filter)
	assert.Equal(t, solana.TokenProgramID.String(), filter.ProgramID)
}

func TestSolana_SignatureAccounts_WithoutTokensIsTheOwnerAlone(t *testing.T) {
	node := mintNode(t, nil)

	got, err := liveOn(node.server.URL).SignatureAccounts(context.Background(), ataFixtureOwner)

	require.NoError(t, err)
	assert.Equal(t, []string{ataFixtureOwner}, got)
	assert.Empty(t, node.requests, "no read is needed")
}

func TestSolana_SignatureAccounts_RefusesAMintItCannotPlace(t *testing.T) {
	cases := map[string]struct {
		mintAnswer string
		reachable  bool
	}{
		"no such account":        {`"result":{"context":{"slot":1},"value":null}`, true},
		"owned by a non-token":   {`"result":{"context":{"slot":1},"value":{"data":["","base64"],"owner":"11111111111111111111111111111111","lamports":1,"executable":false}}`, true},
		"the read keeps failing": {"", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var reads int
			var mu sync.Mutex
			node := newMethodNode(t, func(req solanaRequest) string {
				mu.Lock()
				defer mu.Unlock()
				if req.Method == "getAccountInfo" {
					reads++
					return tc.mintAnswer
				}
				return `"result":{"context":{"slot":1},"value":[]}`
			})
			live := liveOn(node.server.URL, devnetUSDC)

			_, err := live.SignatureAccounts(context.Background(), ataFixtureOwner)
			require.Error(t, err)
			_, err = live.SignatureAccounts(context.Background(), ataFixtureOwner)
			require.Error(t, err)
			mu.Lock()
			defer mu.Unlock()
			assert.GreaterOrEqual(t, reads, 2, "a mint that could not be placed is read again on the next call")
			assert.Empty(t, node.calls("getTokenAccountsByOwner"))
		})
	}
}

func TestSolana_SignatureAccounts_RefusesAnOwnerThatIsNotAKeyAndATokenAccountWithoutItsMint(t *testing.T) {
	node := newMethodNode(t, func(req solanaRequest) string {
		if req.Method == "getAccountInfo" {
			return fixtureResult(t, "getAccountInfo_mint_token.json")
		}
		return `"result":{"context":{"slot":1},"value":[{"account":{"data":["AAEC","base64"]},"pubkey":"` + ataFixtureOtherAccount + `"}]}`
	})
	live := liveOn(node.server.URL, devnetUSDC)

	_, err := live.SignatureAccounts(context.Background(), "not a key")
	assert.ErrorIs(t, err, chain.ErrInvalidAddress)
	_, err = live.SignatureAccounts(context.Background(), ataFixtureOwner)
	assert.Error(t, err, "a token account whose mint cannot be read is never dropped silently")
}

// transfersOf is the transfers of one transaction among a block's.
func transfersOf(transfers []types.DetectedTransfer, signature string) []types.DetectedTransfer {
	var own []types.DetectedTransfer
	for _, transfer := range transfers {
		if transfer.TxHash == signature {
			own = append(own, transfer)
		}
	}
	return own
}

// The signature sync and the block scan must find the same credits at the same
// positions in one transaction, or a deposit both see (or the push and the sync see)
// is recorded twice: positions decide the deposit row (depositrecord).
func TestSolana_TransactionTransfers_FindTheCreditsScanBlockFindsAtTheSamePositions(t *testing.T) {
	cases := []struct {
		name, block, transaction, signature string
		slot                                uint64
		blockhash                           string
		tokens                              []types.Token
		credits                             int
	}{
		{"version 0 with lookup tables", "getBlock_v0_lookup.json", "getTransaction_v0_lookup.json", lookupFixtureSwap,
			lookupFixtureSlot, lookupFixtureBlockhash, lookupTokens, 7},
		{"version 1", "getBlock_v1_spl.json", "getTransaction_v1.json", v1FixtureSwap,
			v1FixtureSlot, v1FixtureBlockhash, []types.Token{devnetUSDC, devnetWSOL}, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			blockLive, _ := serveVersionedSolanaFixture(t, tc.block, tc.tokens...)
			txLive, requests := serveVersionedSolanaFixture(t, tc.transaction, tc.tokens...)
			scanned, err := blockLive.ScanBlock(context.Background(), tc.slot)
			require.NoError(t, err)
			want := transfersOf(scanned, tc.signature)
			require.Len(t, want, tc.credits)

			got, err := txLive.TransactionTransfers(context.Background(), tc.signature)

			require.NoError(t, err)
			assert.Equal(t, tc.slot, got.Slot)
			require.Len(t, got.Transfers, len(want))
			for i := range want {
				assert.Equal(t, tc.blockhash, want[i].BlockHash)
				expected := want[i]
				expected.BlockHash = ""
				assert.Equal(t, expected, got.Transfers[i], "credit %d", i)
			}
			require.Len(t, *requests, 1)
			assert.Equal(t, "getTransaction", (*requests)[0].Method)
			assert.EqualValues(t, solanaMaxSupportedTxVersion, requestedTransactionVersion((*requests)[0]))
		})
	}
}

// getBlock's "accounts" details list a transaction's keys in balance order: the
// static keys, then the lookup-table loaded writable ones, then the readonly ones.
// getTransaction (json) splits them: message.accountKeys and meta.loadedAddresses.
func TestSolana_TransactionTransfers_OrdersTheKeysOfALookupTableTransactionAsItsBlockDoes(t *testing.T) {
	cases := []struct{ block, transaction, signature string }{
		{"getBlock_v0_lookup.json", "getTransaction_v0_lookup.json", lookupFixtureSwap},
		{"getBlock_v1_spl.json", "getTransaction_v0_lookup_fee_only.json", lookupFeeOnlySwap},
	}
	for _, tc := range cases {
		t.Run(tc.signature[:8], func(t *testing.T) {
			var block struct {
				Result solanaBlockAccounts `json:"result"`
			}
			require.NoError(t, json.Unmarshal(readSolanaFixture(t, tc.block), &block))
			var tx struct {
				Result solanaSignedTransaction `json:"result"`
			}
			require.NoError(t, json.Unmarshal(readSolanaFixture(t, tc.transaction), &tx))
			var blockKeys []string
			for _, entry := range block.Result.Transactions {
				if entry.Transaction.Signatures[0] == tc.signature {
					for _, key := range entry.Transaction.AccountKeys {
						blockKeys = append(blockKeys, key.Pubkey)
					}
				}
			}
			require.NotEmpty(t, blockKeys)
			loaded := tx.Result.Meta.LoadedAddresses
			require.NotNil(t, loaded)
			require.NotEmpty(t, loaded.Writable)
			require.NotEmpty(t, loaded.Readonly)
			assert.Len(t, tx.Result.Transaction.Message.AccountKeys, len(blockKeys)-len(loaded.Writable)-len(loaded.Readonly))

			assert.Equal(t, blockKeys, tx.Result.balanceKeys())
		})
	}
}

func TestSolana_TransactionTransfers_CreditsOnLoadedKeysCarryTheirBlockPositions(t *testing.T) {
	live, _ := serveVersionedSolanaFixture(t, "getTransaction_v0_lookup.json", lookupTokens...)

	got, err := live.TransactionTransfers(context.Background(), lookupFixtureSwap)

	require.NoError(t, err)
	positions := make([]int, len(got.Transfers))
	for i, transfer := range got.Transfers {
		positions[i] = transfer.LogIndex
		assert.Equal(t, uint64(lookupFixtureSlot), transfer.BlockNumber)
		assert.Equal(t, int64(lookupFixtureBlockTime), transfer.Timestamp.Unix())
		assert.Empty(t, transfer.BlockHash, "the caller fills the slot's hash")
	}
	// Lamports rose on keys 1, 5 and 8 (5 and 8 loaded from a table: wrapped SOL
	// accounts); the token credits sit after every account index, at 256 + the
	// credited token account: wrapped SOL at 5 and 8, the two other mints at 13 and 16.
	assert.Equal(t, []int{1, 5, 8, 256 + 5, 256 + 8, 256 + 13, 256 + 16}, positions)
}

func TestSolana_TransactionTransfers_RefusesAnAnswerItCannotPlace(t *testing.T) {
	var recorded map[string]any
	require.NoError(t, json.Unmarshal(readSolanaFixture(t, "getTransaction_v0_lookup.json"), &recorded))
	withoutLoaded := recorded["result"].(map[string]any)
	delete(withoutLoaded["meta"].(map[string]any), "loadedAddresses")
	withoutLoadedBody, err := json.Marshal(withoutLoaded)
	require.NoError(t, err)

	cases := map[string]string{
		"not returned yet":               `"result":null`,
		"another transaction":            fixtureResult(t, "getTransaction_v1.json"),
		"the loaded keys are missing":    `"result":` + string(withoutLoadedBody),
		"the node refuses the signature": `"error":{"code":-32602,"message":"Invalid param: Invalid"}`,
		"a meta that is missing":         `"result":{"slot":5,"transaction":{"signatures":["` + lookupFixtureSwap + `"],"message":{"accountKeys":["a"]}},"meta":null}`,
	}
	for name, answer := range cases {
		t.Run(name, func(t *testing.T) {
			node := newMethodNode(t, func(solanaRequest) string { return answer })
			_, readErr := liveOn(node.server.URL, lookupTokens...).TransactionTransfers(context.Background(), lookupFixtureSwap)
			assert.Error(t, readErr, "an answer that cannot be placed is an error, never a transaction without credits")
		})
	}
	_, err = liveOn(refusedSolanaURL(t)).TransactionTransfers(context.Background(), " ")
	assert.Error(t, err)
}

func TestSolana_TransactionTransfers_AFailedTransactionCreditsNothing(t *testing.T) {
	var recorded map[string]any
	require.NoError(t, json.Unmarshal(readSolanaFixture(t, "getTransaction_v0_lookup.json"), &recorded))
	result := recorded["result"].(map[string]any)
	result["meta"].(map[string]any)["err"] = map[string]any{"InstructionError": []any{2, "InvalidAccountData"}}
	body, err := json.Marshal(result)
	require.NoError(t, err)
	node := newMethodNode(t, func(solanaRequest) string { return `"result":` + string(body) })

	got, err := liveOn(node.server.URL, lookupTokens...).TransactionTransfers(context.Background(), lookupFixtureSwap)

	require.NoError(t, err)
	assert.Equal(t, uint64(lookupFixtureSlot), got.Slot)
	assert.Empty(t, got.Transfers)
}

func TestSolana_TransactionTransfers_ARegisteredMintThatIsNotAKeyFailsEveryRead(t *testing.T) {
	live := liveOn(refusedSolanaURL(t), types.Token{Symbol: "BAD", Contract: "not-a-mint", ChainID: models.ChainSOL})

	_, err := live.TransactionTransfers(context.Background(), lookupFixtureSwap)
	assert.Error(t, err)
	_, err = live.SignatureAccounts(context.Background(), ataFixtureOwner)
	assert.Error(t, err)
}
