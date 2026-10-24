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

// pageCalls are the getSignaturesForAddress calls of full pages, and confirmations
// the single-signature calls that confirm a short page reached its cursor.
func (n *methodNode) pageCalls(t *testing.T) (pages, confirmations []solanaRequest) {
	t.Helper()
	for _, req := range n.calls("getSignaturesForAddress") {
		var options struct{ Limit int }
		req.param(t, 1, &options)
		if options.Limit == 1 {
			confirmations = append(confirmations, req)
			continue
		}
		pages = append(pages, req)
	}
	return pages, confirmations
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
	confirmation := options.Limit == 1 && options.Until == ""
	if options.Commitment != solanaCommitmentFinalized || (options.Limit != solanaSignaturePageLimit && !confirmation) {
		t.Errorf("getSignaturesForAddress options %+v, want finalized and %d per page (or 1, confirming a cursor)", options, solanaSignaturePageLimit)
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

// historyNode keeps history and answers its pages, its finalized slot (head) and its
// first available block (ledgerStart).
func historyNode(t *testing.T, history *accountHistory, head, ledgerStart uint64) *methodNode {
	return newMethodNode(t, func(req solanaRequest) string {
		switch req.Method {
		case "getSlot":
			return fmt.Sprintf(`"result":%d`, head)
		case "getFirstAvailableBlock":
			return fmt.Sprintf(`"result":%d`, ledgerStart)
		case "getSignaturesForAddress":
			return history.page(t, req)
		}
		return `"error":{"code":-32601,"message":"Method not found"}`
	})
}

func TestSolana_AccountSignatures_PagesBackUntilTheCursorNewestFirst(t *testing.T) {
	history := newAccountHistory(2600, 9000)
	node := historyNode(t, history, 9100, 0)
	// sig-00399 is the newest signature already processed: the 2200 newer ones are new.
	until := "sig-00399"

	got, err := liveOn(node.server.URL).AccountSignatures(context.Background(), ataFixtureOwner, chain.SignatureQuery{Until: until, MaxPages: 10})

	require.NoError(t, err)
	require.Len(t, got.Signatures, 2200)
	assert.True(t, got.Ended, "a short page with the cursor reaches it")
	assert.False(t, got.More)
	assert.Equal(t, uint64(9100), got.Head, "the endpoint's finalized slot, read before the first page")
	assert.Equal(t, "sig-02599", got.Signatures[0].Signature, "newest first")
	assert.Equal(t, "sig-00400", got.Signatures[2199].Signature, "the cursor left out")
	for i := 1; i < len(got.Signatures); i++ {
		require.GreaterOrEqual(t, got.Signatures[i-1].Slot, got.Signatures[i].Slot)
	}
	pages, confirmations := node.pageCalls(t)
	require.Len(t, pages, 3, "1000 + 1000 + 200: the short page ends at the cursor")
	assert.Empty(t, node.calls("getFirstAvailableBlock"), "a recent cursor needs no ledger evidence")
	require.Len(t, confirmations, 1, "the endpoint confirms the short page reached the cursor")
	var confirm struct {
		Before, Until string
		Limit         int
	}
	confirmations[0].param(t, 1, &confirm)
	assert.Equal(t, "sig-00400", confirm.Before, "the one signature below the oldest listed")
	assert.Empty(t, confirm.Until)
	assert.Equal(t, 1, confirm.Limit)
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

// A provider that does not hold the cursor may answer a short or empty page instead
// of failing (Agave fails it): the signature below what it listed is then not the
// cursor, so the listing did not reach it, and says so.
func TestSolana_AccountSignatures_AShortPageAboveACursorTheEndpointDoesNotHoldIsNoEnd(t *testing.T) {
	for name, tc := range map[string]struct {
		count      int
		wantBefore string
	}{
		"a short page": {count: 40, wantBefore: "sig-00000"},
		"an empty one": {count: 0},
	} {
		t.Run(name, func(t *testing.T) {
			node := historyNode(t, newAccountHistory(tc.count, 9000), 9100, 0)

			got, err := liveOn(node.server.URL).AccountSignatures(context.Background(), ataFixtureOwner,
				chain.SignatureQuery{Until: "sig-the-endpoint-does-not-hold", MaxPages: 10})

			require.NoError(t, err)
			assert.Len(t, got.Signatures, tc.count)
			assert.False(t, got.Ended, "nothing proves the cursor was reached")
			assert.False(t, got.More)
			_, confirmations := node.pageCalls(t)
			require.Len(t, confirmations, 1)
			var confirm struct{ Before string }
			confirmations[0].param(t, 1, &confirm)
			assert.Equal(t, tc.wantBefore, confirm.Before, "below the oldest listed, or from the newest when none was")
		})
	}
}

// A gap with nothing in it (the signature below before is the cursor) answers an
// empty page; the confirmation, below before, finds the cursor and closes it.
func TestSolana_AccountSignatures_ClosesAnEmptyGapBelowBefore(t *testing.T) {
	node := historyNode(t, newAccountHistory(10, 9000), 9100, 0)

	got, err := liveOn(node.server.URL).AccountSignatures(context.Background(), ataFixtureOwner,
		chain.SignatureQuery{Until: "sig-00004", Before: "sig-00005", Anchor: "sig-00006", MaxPages: 10})

	require.NoError(t, err)
	assert.Empty(t, got.Signatures, "the before itself is not returned")
	assert.True(t, got.Ended, "the signature below before is the cursor")
	pages, confirmations := node.pageCalls(t)
	require.Len(t, pages, 1)
	var page struct{ Before string }
	pages[0].param(t, 1, &page)
	assert.Equal(t, "sig-00006", page.Before, "read from right below the anchor, which places the before first")
	require.Len(t, confirmations, 1)
	var confirm struct{ Before string }
	confirmations[0].param(t, 1, &confirm)
	assert.Equal(t, "sig-00005", confirm.Before, "below the before when nothing else was listed")
}

// The confirmation of a cursor is asked of the endpoint that served the listing: when
// it fails there, the listing fails, and no other endpoint answers it.
func TestSolana_AccountSignatures_ConfirmsTheCursorOnTheEndpointThatServedThePages(t *testing.T) {
	history := newAccountHistory(40, 9000)
	primary := newMethodNode(t, func(req solanaRequest) string {
		switch req.Method {
		case "getGenesisHash":
			return devnetGenesisResult
		case "getSlot":
			return `"result":9100`
		case "getSignaturesForAddress":
			var options struct{ Limit int }
			req.param(t, 1, &options)
			if options.Limit == 1 {
				return ""
			}
			return history.page(t, req)
		}
		return ""
	})
	fallback := newMethodNode(t, func(req solanaRequest) string {
		switch req.Method {
		case "getGenesisHash":
			return devnetGenesisResult
		case "getSlot":
			return `"result":9100`
		}
		return `"result":[{"signature":"sig-00010","slot":8990,"err":null}]`
	})
	live := devnetWithFallbacks(primary.server.URL, fallback.server.URL)

	_, err := live.AccountSignatures(context.Background(), ataFixtureOwner, chain.SignatureQuery{Until: "sig-00010", MaxPages: 10})

	require.Error(t, err, "the endpoint that served the pages did not confirm the cursor")
	assert.Empty(t, fallback.calls("getSignaturesForAddress"), "no other endpoint confirms this listing's cursor")
}

// busyAccountNode keeps history and, right before it answers the first confirmation
// asked from the newest signature (the one of an empty page), lands a new signature
// on the account: a transfer finalized between the page and its confirmation. With
// failRelisting it then fails the next listing (HTTP 503).
func busyAccountNode(t *testing.T, history *accountHistory, failRelisting bool) *methodNode {
	t.Helper()
	var mu sync.Mutex
	landed, failed := false, false
	return newMethodNode(t, func(req solanaRequest) string {
		switch req.Method {
		case "getGenesisHash":
			return devnetGenesisResult
		case "getSlot":
			return `"result":9100`
		case "getSignaturesForAddress":
			mu.Lock()
			defer mu.Unlock()
			var options struct {
				Before string
				Limit  int
			}
			req.param(t, 1, &options)
			if options.Limit == 1 && options.Before == "" && !landed {
				landed = true
				history.signatures = append([]solanaSignatureInfo{{Signature: "sig-landed", Slot: 9001}}, history.signatures...)
			} else if failRelisting && landed && !failed && options.Limit == solanaSignaturePageLimit {
				failed = true
				return ""
			}
			return history.page(t, req)
		}
		return ""
	})
}

// An empty page above the newest recorded signature is confirmed from the newest
// one, so a signature that lands in between answers instead of the cursor (a busy
// account). The listing is read again once on the same endpoint: it lists the new
// signature, whose confirmation, below it, is the cursor itself.
func TestSolana_AccountSignatures_ListsAgainOnceWhenASignatureLandsBeforeAnEmptyPageIsConfirmed(t *testing.T) {
	node := busyAccountNode(t, newAccountHistory(10, 9000), false)

	got, err := liveOn(node.server.URL).AccountSignatures(context.Background(), ataFixtureOwner,
		chain.SignatureQuery{Until: "sig-00009", FloorSlot: 9000, HasFloor: true, MaxPages: 1})

	require.NoError(t, err)
	assert.True(t, got.Ended, "the listing read again reached the cursor, confirmed below the signature that landed")
	assert.False(t, got.More, "the empty page read nothing and is not counted against MaxPages")
	require.Len(t, got.Signatures, 1)
	assert.Equal(t, "sig-landed", got.Signatures[0].Signature)
	assert.Equal(t, uint64(9100), got.Head)
	pages, confirmations := node.pageCalls(t)
	require.Len(t, pages, 2, "the empty page, then the listing read again once")
	for i, page := range pages {
		var options struct{ Before, Until string }
		page.param(t, 1, &options)
		assert.Equal(t, "sig-00009", options.Until, "page %d keeps the cursor", i)
		assert.Empty(t, options.Before, "page %d starts from the newest signature", i)
	}
	require.Len(t, confirmations, 2)
	var first, second struct{ Before string }
	confirmations[0].param(t, 1, &first)
	confirmations[1].param(t, 1, &second)
	assert.Empty(t, first.Before, "the empty page is confirmed from the newest signature")
	assert.Equal(t, "sig-landed", second.Before, "the page read again is confirmed below what it listed")
	assert.Len(t, node.calls("getSlot"), 1, "the head is read once, before the first page")
}

// The listing read again stays on the endpoint the listing is pinned to: when that
// endpoint fails it, the listing fails, and no other endpoint answers it.
func TestSolana_AccountSignatures_ListsAgainOnTheEndpointThatServedTheEmptyPage(t *testing.T) {
	primary := busyAccountNode(t, newAccountHistory(10, 9000), true)
	fallback := newMethodNode(t, func(req solanaRequest) string {
		switch req.Method {
		case "getGenesisHash":
			return devnetGenesisResult
		case "getSlot":
			return `"result":9100`
		}
		return `"result":[]`
	})
	live := devnetWithFallbacks(primary.server.URL, fallback.server.URL)

	_, err := live.AccountSignatures(context.Background(), ataFixtureOwner, chain.SignatureQuery{Until: "sig-00009", MaxPages: 10})

	require.Error(t, err, "the endpoint that served the empty page failed the listing read again")
	assert.Empty(t, fallback.calls("getSignaturesForAddress"), "no other endpoint lists or confirms it")
}

// Reading the listing again never weakens the proof: the confirmation of its page
// must still be the cursor itself, and it is read again only once.
func TestSolana_AccountSignatures_ListsAgainOnlyOnceAndStillNeedsTheCursorItself(t *testing.T) {
	for name, tc := range map[string]struct {
		// listing answers a page with until; confirmation the single signature asked.
		listing      func(before string) string
		confirmation func(before string) string
		wantPages    int
		wantListed   int
	}{
		// A provider that answers an empty page for a cursor it does not hold, while the
		// account's newest signature is another one.
		"an empty page again": {
			listing:      func(string) string { return `"result":[]` },
			confirmation: func(string) string { return `"result":[{"signature":"sig-newest","slot":9050,"err":null}]` },
			wantPages:    2,
		},
		// The page read again lists a signature, but the one below it is not the cursor.
		"a short page whose confirmation is another signature": {
			listing: func(string) string { return `"result":[{"signature":"sig-landed","slot":9001,"err":null}]` },
			confirmation: func(before string) string {
				if before == "sig-landed" {
					return `"result":[{"signature":"sig-not-the-cursor","slot":8999,"err":null}]`
				}
				return `"result":[{"signature":"sig-landed","slot":9001,"err":null}]`
			},
			wantPages:  2,
			wantListed: 1,
		},
	} {
		t.Run(name, func(t *testing.T) {
			listings := 0
			node := newMethodNode(t, func(req solanaRequest) string {
				switch req.Method {
				case "getSlot":
					return `"result":9100`
				case "getSignaturesForAddress":
					var options struct {
						Before string
						Limit  int
					}
					req.param(t, 1, &options)
					if options.Limit == 1 {
						return tc.confirmation(options.Before)
					}
					listings++
					if listings == 1 {
						return `"result":[]`
					}
					return tc.listing(options.Before)
				}
				return `"error":{"code":-32601,"message":"Method not found"}`
			})

			got, err := liveOn(node.server.URL).AccountSignatures(context.Background(), ataFixtureOwner,
				chain.SignatureQuery{Until: "sig-cursor", MaxPages: 10})

			require.NoError(t, err)
			assert.False(t, got.Ended, "nothing answered the cursor itself")
			assert.Len(t, got.Signatures, tc.wantListed)
			pages, confirmations := node.pageCalls(t)
			assert.Len(t, pages, tc.wantPages, "the listing is read again once, never more")
			assert.Len(t, confirmations, 2)
		})
	}
}

// A listing below a before reads its first page from right below the anchor (the
// signature recorded right above the before) and must find the before itself first.
// An address index that does not hold the before there may answer an empty page, its
// own newest signatures, a page where another signature follows the anchor, or
// Agave's -32020: whatever it answers, nothing is listed or ended, so an empty page
// never reads as the end of a history and nothing older than the before is skipped,
// and no other call is made.
func TestSolana_AccountSignatures_ListsNothingBelowABeforeItsIndexDoesNotPlace(t *testing.T) {
	for name, listing := range map[string]string{
		"an empty page":                      `"result":[]`,
		"its own newest signature":           `"result":[{"signature":"sig-older","slot":4700,"err":null}]`,
		"another signature below the anchor": `"result":[{"signature":"sig-between","slot":4950,"err":null},{"signature":"sig-before","slot":4940,"err":null}]`,
		"Agave's -32020":                     `"error":{"code":-32020,"message":"Transaction sig-anchor not found"}`,
	} {
		for _, until := range []string{"", "sig-cursor"} {
			t.Run(fmt.Sprintf("%s, until %q", name, until), func(t *testing.T) {
				node := newMethodNode(t, func(req solanaRequest) string {
					switch req.Method {
					case "getSlot":
						return `"result":9100`
					case "getFirstAvailableBlock":
						return `"result":0`
					case "getSignaturesForAddress":
						return listing
					}
					return `"error":{"code":-32601,"message":"Method not found"}`
				})

				got, err := liveOn(node.server.URL).AccountSignatures(context.Background(), ataFixtureOwner,
					chain.SignatureQuery{Before: "sig-before", Anchor: "sig-anchor", Until: until, FloorSlot: 1000, HasFloor: true, MaxPages: 5})

				require.NoError(t, err)
				assert.Empty(t, got.Signatures)
				assert.False(t, got.Ended, "nothing proves the end below a before the index does not place")
				assert.False(t, got.More)
				assert.False(t, got.FloorCrossed)
				assert.Equal(t, uint64(9100), got.Head)
				listings := node.calls("getSignaturesForAddress")
				require.Len(t, listings, 1, "the first page decides; nothing is listed below it")
				var options struct{ Before string }
				listings[0].param(t, 1, &options)
				assert.Equal(t, "sig-anchor", options.Before, "read from right below the anchor")
				assert.Empty(t, node.calls("getFirstAvailableBlock"))
				assert.Empty(t, node.calls("getSignatureStatuses"), "the index itself is the proof; no status is asked")
			})
		}
	}
}

// A placed before is left out, and the listing goes on below it page by page, each
// page from below the last signature the index listed, down to the end of the
// history: one fewer new signature on the first page, no call of its own.
func TestSolana_AccountSignatures_PagesDownBelowAPlacedBefore(t *testing.T) {
	history := newAccountHistory(3000, 9000)
	node := historyNode(t, history, 9100, 0)

	got, err := liveOn(node.server.URL).AccountSignatures(context.Background(), ataFixtureOwner,
		chain.SignatureQuery{Before: "sig-02500", Anchor: "sig-02501", MaxPages: 10})

	require.NoError(t, err)
	require.Len(t, got.Signatures, 2500)
	assert.Equal(t, "sig-02499", got.Signatures[0].Signature)
	assert.Equal(t, "sig-00000", got.Signatures[2499].Signature)
	for i := 1; i < len(got.Signatures); i++ {
		require.GreaterOrEqual(t, got.Signatures[i-1].Slot, got.Signatures[i].Slot)
	}
	assert.True(t, got.Ended, "the empty page below the oldest")
	pages, _ := node.pageCalls(t)
	require.Len(t, pages, 4, "1000 (the before and 999), 1000, 501, then the empty page")
	for i, want := range []string{"sig-02501", "sig-01501", "sig-00501", "sig-00000"} {
		var options struct{ Before string }
		pages[i].param(t, 1, &options)
		assert.Equal(t, want, options.Before, "page %d", i)
	}
	assert.Len(t, node.calls("getFirstAvailableBlock"), 1)
}

// A before recorded without an anchor (the only signature of its stretch) is read
// from the newest signature, which must be that before: an index whose newest is
// another signature places nothing.
func TestSolana_AccountSignatures_ABeforeWithoutAnAnchorMustBeTheNewestSignature(t *testing.T) {
	history := newAccountHistory(10, 9000)
	node := historyNode(t, history, 9100, 0)

	got, err := liveOn(node.server.URL).AccountSignatures(context.Background(), ataFixtureOwner,
		chain.SignatureQuery{Before: "sig-00009", FloorSlot: 8000, HasFloor: true, MaxPages: 5})
	require.NoError(t, err)
	require.Len(t, got.Signatures, 9, "everything below the newest, which is the before")
	assert.Equal(t, "sig-00008", got.Signatures[0].Signature)
	var options struct{ Before string }
	node.calls("getSignaturesForAddress")[0].param(t, 1, &options)
	assert.Empty(t, options.Before, "read from the newest signature")

	moved, err := liveOn(node.server.URL).AccountSignatures(context.Background(), ataFixtureOwner,
		chain.SignatureQuery{Before: "sig-00005", FloorSlot: 8000, HasFloor: true, MaxPages: 5})
	require.NoError(t, err)
	assert.Empty(t, moved.Signatures, "newer signatures sit above the before: it is not placed")
	assert.False(t, moved.Ended)
}

// A failed first page of a listing below a before is the error it is, never a
// placed before; on a client with fallbacks it stays on the endpoint the listing is
// pinned to, and no other endpoint answers for it.
func TestSolana_AccountSignatures_AFailedAnchoredPageIsAnErrorNotAPlacedBefore(t *testing.T) {
	for name, failure := range map[string]string{
		"an internal error": `"error":{"code":-32603,"message":"Internal error"}`,
		"HTTP 503":          "",
	} {
		t.Run(name, func(t *testing.T) {
			history := newAccountHistory(40, 9000)
			primary := newMethodNode(t, func(req solanaRequest) string {
				switch req.Method {
				case "getGenesisHash":
					return devnetGenesisResult
				case "getSlot":
					return `"result":9100`
				case "getSignaturesForAddress":
					return failure
				}
				return ""
			})
			fallback := newMethodNode(t, func(req solanaRequest) string {
				switch req.Method {
				case "getGenesisHash":
					return devnetGenesisResult
				case "getSlot":
					return `"result":9100`
				case "getSignaturesForAddress":
					return history.page(t, req)
				}
				return `"result":0`
			})
			live := devnetWithFallbacks(primary.server.URL, fallback.server.URL)

			got, err := live.AccountSignatures(context.Background(), ataFixtureOwner,
				chain.SignatureQuery{Before: "sig-00020", Anchor: "sig-00021", FloorSlot: 8000, HasFloor: true, MaxPages: 10})

			require.Error(t, err, "a failed page is no proof that the index holds the before")
			assert.Empty(t, got.Signatures)
			assert.False(t, got.Ended)
			assert.Empty(t, fallback.calls("getSignaturesForAddress"), "no other endpoint answers for the pinned listing")
		})
	}
}

func TestSolana_AccountSignatures_ContinuesBelowBefore(t *testing.T) {
	history := newAccountHistory(1500, 9000)
	node := historyNode(t, history, 9100, 0)

	got, err := liveOn(node.server.URL).AccountSignatures(context.Background(), ataFixtureOwner,
		chain.SignatureQuery{Before: "sig-00600", Anchor: "sig-00601", FloorSlot: 8500, HasFloor: true, MaxPages: 10})

	require.NoError(t, err)
	require.NotEmpty(t, got.Signatures)
	assert.Equal(t, "sig-00599", got.Signatures[0].Signature, "the listing resumes right below before, which is left out")
	var first struct{ Before string }
	node.calls("getSignaturesForAddress")[0].param(t, 1, &first)
	assert.Equal(t, "sig-00601", first.Before, "the first page starts right below the anchor")
}

func TestSolana_AccountSignatures_StopsAtTheFirstSignatureBelowTheFloor(t *testing.T) {
	history := newAccountHistory(3000, 9000)
	node := historyNode(t, history, 9100, 0)
	// Two signatures per slot from 9000 down: slot 8250 holds sig-01499 and sig-01498,
	// on the second page; slot 8249 is below the floor.
	got, err := liveOn(node.server.URL).AccountSignatures(context.Background(), ataFixtureOwner,
		chain.SignatureQuery{FloorSlot: 8250, HasFloor: true, MaxPages: 10})

	require.NoError(t, err)
	assert.True(t, got.Ended)
	assert.True(t, got.FloorCrossed, "the floor proves the end, whatever the endpoint's ledger")
	require.Len(t, got.Signatures, 1502, "slots 9000 down to 8250, the floor slot included")
	assert.Equal(t, uint64(8250), got.Signatures[1501].Slot)
	assert.Len(t, node.calls("getSignaturesForAddress"), 2, "no page is read past the floor")
	assert.Empty(t, node.calls("getFirstAvailableBlock"))
}

func TestSolana_AccountSignatures_CutsAHistoryLongerThanMaxPages(t *testing.T) {
	history := newAccountHistory(2500, 9000)
	node := historyNode(t, history, 9100, 0)

	got, err := liveOn(node.server.URL).AccountSignatures(context.Background(), ataFixtureOwner, chain.SignatureQuery{MaxPages: 2})

	require.NoError(t, err)
	assert.True(t, got.More, "500 older signatures were not read")
	assert.False(t, got.Ended)
	require.Len(t, got.Signatures, 2000)
	assert.Equal(t, "sig-00500", got.Signatures[1999].Signature, "the newest pages are kept")
	assert.Len(t, node.calls("getSignaturesForAddress"), 2)
}

// A node that does not hold an old ledger (a non-archival node, or a failover to one)
// answers a short page, with no error, where the history goes on: without the cursor
// only an empty page ends a listing, and the endpoint's first available block says
// how far down its ledger goes.
func TestSolana_AccountSignatures_AShortPageWithoutTheCursorIsNoEndOfTheHistory(t *testing.T) {
	pages := [][]string{{"sig-c", "sig-b"}, {"sig-a"}, {}}
	var served int
	var mu sync.Mutex
	node := newMethodNode(t, func(req solanaRequest) string {
		switch req.Method {
		case "getSlot":
			return `"result":9100`
		case "getFirstAvailableBlock":
			return `"result":512000000`
		case "getSignaturesForAddress":
			mu.Lock()
			defer mu.Unlock()
			page := pages[min(served, len(pages)-1)]
			served++
			entries := make([]string, len(page))
			for i, signature := range page {
				entries[i] = fmt.Sprintf(`{"signature":%q,"slot":%d,"err":null}`, signature, 9000-served)
			}
			return `"result":[` + strings.Join(entries, ",") + `]`
		}
		return `"error":{"code":-32601,"message":"Method not found"}`
	})

	got, err := liveOn(node.server.URL).AccountSignatures(context.Background(), ataFixtureOwner,
		chain.SignatureQuery{FloorSlot: 100, HasFloor: true, MaxPages: 10})

	require.NoError(t, err)
	assert.Equal(t, []string{"sig-c", "sig-b", "sig-a"}, []string{got.Signatures[0].Signature, got.Signatures[1].Signature, got.Signatures[2].Signature},
		"the page after a short one is read")
	assert.True(t, got.Ended, "an empty page ends the listing")
	assert.False(t, got.FloorCrossed)
	assert.Equal(t, uint64(512000000), got.LedgerStart, "the ledger of this endpoint starts far above the floor: nothing proves the history ends")
	assert.Len(t, node.calls("getSignaturesForAddress"), 3)
	assert.Len(t, node.calls("getFirstAvailableBlock"), 1)
}

func TestSolana_AccountSignatures_ReadsTheRecordedPageAndMarksFailedTransactions(t *testing.T) {
	var page []map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(fixtureResult(t, "getSignaturesForAddress_page.json"), `"result":`)), &page))
	require.Len(t, page, 10)
	page[3]["err"] = map[string]any{"InstructionError": []any{0, map[string]any{"Custom": 6022}}}
	body, err := json.Marshal(page)
	require.NoError(t, err)
	node := newMethodNode(t, func(req solanaRequest) string {
		switch req.Method {
		case "getSlot":
			return `"result":514240000`
		case "getFirstAvailableBlock":
			return `"result":0`
		}
		var options struct{ Before string }
		req.param(t, 1, &options)
		if options.Before != "" {
			return `"result":[]`
		}
		return `"result":` + string(body)
	})

	got, err := liveOn(node.server.URL).AccountSignatures(context.Background(), ataFixtureOwner, chain.SignatureQuery{MaxPages: 3})

	require.NoError(t, err)
	assert.True(t, got.Ended)
	assert.Zero(t, got.LedgerStart)
	require.Len(t, got.Signatures, 10)
	newest := got.Signatures[0]
	assert.Equal(t, "2yMCe5i8u3jpxnMr18chCCZxP3JDDbj2WJD4SzbpTjb7643q9Enfidtx12eJK8pHuiN11eHTqn69C7FE9kSVpt2o", newest.Signature)
	assert.Equal(t, uint64(514235140), newest.Slot)
	for i, signature := range got.Signatures {
		assert.Equal(t, i == 3, signature.Failed, "only the entry with an err is a failed transaction (signature %d)", i)
	}
}

func TestSolana_AccountSignatures_RefusesAMalformedQueryOrAnswer(t *testing.T) {
	node := newMethodNode(t, func(req solanaRequest) string {
		if req.Method == "getSlot" {
			return `"result":9100`
		}
		return `"result":[{"signature":"","slot":5,"err":null}]`
	})
	live := liveOn(node.server.URL)

	_, err := live.AccountSignatures(context.Background(), "not a key", chain.SignatureQuery{MaxPages: 1})
	assert.ErrorIs(t, err, chain.ErrInvalidAddress)
	_, err = live.AccountSignatures(context.Background(), ataFixtureOwner, chain.SignatureQuery{})
	assert.Error(t, err, "a query that allows no page reads nothing")
	_, err = live.AccountSignatures(context.Background(), ataFixtureOwner, chain.SignatureQuery{MaxPages: 1})
	assert.ErrorContains(t, err, "no signature", "an entry without a signature is an error")
}

// A node that does not know the signature a page starts before answers an empty
// page, which ends a listing: every call of one account's listing goes to the
// endpoint that answered the first one, and its failure fails the listing.
func TestSolana_AccountSignatures_ReadsEveryPageOfAnAccountFromOneEndpoint(t *testing.T) {
	history := newAccountHistory(1500, 9000)
	var primaryPages int
	var mu sync.Mutex
	primary := newMethodNode(t, func(req solanaRequest) string {
		switch req.Method {
		case "getGenesisHash":
			return devnetGenesisResult
		case "getSlot":
			return `"result":9100`
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
		switch req.Method {
		case "getGenesisHash":
			return devnetGenesisResult
		case "getSlot":
			return `"result":9100`
		case "getFirstAvailableBlock":
			return `"result":0`
		}
		return `"result":[]`
	})
	live := devnetWithFallbacks(primary.server.URL, fallback.server.URL)

	_, err := live.AccountSignatures(context.Background(), ataFixtureOwner, chain.SignatureQuery{MaxPages: 10})

	require.Error(t, err, "the second page failed on the endpoint that served the first")
	assert.Empty(t, fallback.calls("getSignaturesForAddress"), "no page comes from another endpoint")
	assert.Empty(t, fallback.calls("getFirstAvailableBlock"))
}

// The head a listing reports is the finalized slot of the endpoint that serves its
// pages: once getSlot answered, a listing that endpoint fails is an error, never pages
// from another endpoint read against that head.
func TestSolana_AccountSignatures_ReadsTheHeadFromTheEndpointThatServesThePages(t *testing.T) {
	primary := newMethodNode(t, func(req solanaRequest) string {
		switch req.Method {
		case "getGenesisHash":
			return devnetGenesisResult
		case "getSlot":
			return `"result":9100`
		}
		return ""
	})
	fallback := newMethodNode(t, func(req solanaRequest) string {
		switch req.Method {
		case "getGenesisHash":
			return devnetGenesisResult
		case "getSlot":
			return `"result":9000`
		case "getFirstAvailableBlock":
			return `"result":0`
		}
		return `"result":[]`
	})
	live := devnetWithFallbacks(primary.server.URL, fallback.server.URL)

	_, err := live.AccountSignatures(context.Background(), ataFixtureOwner, chain.SignatureQuery{MaxPages: 10})

	require.Error(t, err, "the endpoint that answered the head failed the listing")
	assert.Len(t, primary.calls("getSlot"), 1)
	assert.Empty(t, fallback.calls("getSignaturesForAddress"), "no page is read against another endpoint's head")
}

// The first available block that proves the end of a history is the one of the
// endpoint that served its empty page: when it does not answer, the end is not proven.
func TestSolana_AccountSignatures_ReadsTheLedgerStartFromTheEndpointThatServesThePages(t *testing.T) {
	primary := newMethodNode(t, func(req solanaRequest) string {
		switch req.Method {
		case "getGenesisHash":
			return devnetGenesisResult
		case "getSlot":
			return `"result":9100`
		case "getSignaturesForAddress":
			return `"result":[]`
		}
		return ""
	})
	fallback := newMethodNode(t, func(req solanaRequest) string {
		switch req.Method {
		case "getGenesisHash":
			return devnetGenesisResult
		case "getSlot":
			return `"result":9100`
		case "getFirstAvailableBlock":
			return `"result":0`
		}
		return `"result":[]`
	})
	live := devnetWithFallbacks(primary.server.URL, fallback.server.URL)

	_, err := live.AccountSignatures(context.Background(), ataFixtureOwner, chain.SignatureQuery{MaxPages: 10})

	require.Error(t, err, "the endpoint that served the empty page did not answer its first available block")
	assert.Len(t, primary.calls("getFirstAvailableBlock"), 1)
	assert.Empty(t, fallback.calls("getFirstAvailableBlock"), "another endpoint's ledger proves nothing of this listing")
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
	assert.Empty(t, got.SkippedMints)
	assert.Equal(t, []string{ataFixtureOwner, ataFixtureToken2022, ataFixtureUSDC, ataFixtureOtherAccount}, got.Accounts,
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
	assert.Equal(t, []string{ataFixtureOwner, ataFixtureUSDC, ataFixtureOtherAccount}, got.Accounts)
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
	assert.Equal(t, []string{ataFixtureOwner}, got.Accounts)
	assert.Empty(t, node.requests, "no read is needed")
}

// Mainnet USDC and USDT, registered on a devnet chain by mistake: devnet holds the
// first as an account the System program owns and does not hold the second.
const (
	mainnetUSDCMint = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"
	mainnetUSDTMint = "Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB"
)

func TestSolana_SignatureAccounts_SkipsAMintTheNodeDoesNotHoldAsATokenMintAndReadsTheOthers(t *testing.T) {
	answers := map[string]string{
		devnetUSDCMint:  fixtureResult(t, "getAccountInfo_mint_token.json"),
		mainnetUSDCMint: `"result":{"context":{"slot":1},"value":{"data":["","base64"],"owner":"11111111111111111111111111111111","lamports":1,"executable":false,"space":0}}`,
		mainnetUSDTMint: `"result":{"context":{"slot":1},"value":null}`,
	}
	reads := map[string]int{}
	var mu sync.Mutex
	node := newMethodNode(t, func(req solanaRequest) string {
		switch req.Method {
		case "getAccountInfo":
			var mint string
			req.param(t, 0, &mint)
			mu.Lock()
			reads[mint]++
			mu.Unlock()
			return answers[mint]
		case "getTokenAccountsByOwner":
			usdc := solana.MustPublicKeyFromBase58(devnetUSDCMint)
			return fmt.Sprintf(`"result":{"context":{"slot":1},"value":[{"account":{"data":["%s","base64"]},"pubkey":"%s"}]}`,
				base64.StdEncoding.EncodeToString(usdc[:]), ataFixtureOtherAccount)
		}
		return `"error":{"code":-32601,"message":"Method not found"}`
	})
	live := liveOn(node.server.URL, devnetUSDC,
		types.Token{Symbol: "USDC-MAINNET", Contract: mainnetUSDCMint, Decimals: 6, ChainID: models.ChainSOL},
		types.Token{Symbol: "USDT", Contract: mainnetUSDTMint, Decimals: 6, ChainID: models.ChainSOL})

	for call := 0; call < 2; call++ {
		got, err := live.SignatureAccounts(context.Background(), ataFixtureOwner)

		require.NoError(t, err, "one bad registry row never stops the owner")
		assert.Equal(t, []string{ataFixtureOwner, ataFixtureUSDC, ataFixtureOtherAccount}, got.Accounts, "the other mint is read")
		assert.Equal(t, []string{mainnetUSDCMint, mainnetUSDTMint}, got.SkippedMints)
	}
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, reads[mainnetUSDCMint], "a mint the node does not hold is not asked about again at once")
	assert.Equal(t, 1, reads[mainnetUSDTMint])
	assert.Equal(t, 1, reads[devnetUSDCMint])
}

func TestSolana_SignatureAccounts_FailsWhileAMintCannotBeRead(t *testing.T) {
	var reads int
	var mu sync.Mutex
	node := newMethodNode(t, func(req solanaRequest) string {
		if req.Method == "getAccountInfo" {
			mu.Lock()
			defer mu.Unlock()
			reads++
			return ""
		}
		return `"result":{"context":{"slot":1},"value":[]}`
	})
	live := liveOn(node.server.URL, devnetUSDC)

	_, err := live.SignatureAccounts(context.Background(), ataFixtureOwner)
	require.Error(t, err, "a node that does not answer proves nothing about the mint")
	_, err = live.SignatureAccounts(context.Background(), ataFixtureOwner)
	require.Error(t, err)
	mu.Lock()
	defer mu.Unlock()
	assert.GreaterOrEqual(t, reads, 2, "a mint that could not be read is read again on the next call")
	assert.Empty(t, node.calls("getTokenAccountsByOwner"))
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
