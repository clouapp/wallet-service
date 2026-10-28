package solana

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/gagliardetto/solana-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/services/chain"
)

// stateNode answers getMultipleAccounts from accounts (an account it does not hold
// answers null), at slots taken in turn from slots.
type stateNode struct {
	mu       sync.Mutex
	accounts map[string]string // account → the JSON of its value
	slots    []uint64
}

func (n *stateNode) answer(t *testing.T, req solanaRequest) string {
	t.Helper()
	if req.Method != "getMultipleAccounts" {
		return `"error":{"code":-32601,"message":"Method not found"}`
	}
	var keys []string
	req.param(t, 0, &keys)
	n.mu.Lock()
	defer n.mu.Unlock()
	slot := n.slots[0]
	if len(n.slots) > 1 {
		n.slots = n.slots[1:]
	}
	values := make([]string, len(keys))
	for i, key := range keys {
		value, ok := n.accounts[key]
		if !ok {
			value = "null"
		}
		values[i] = value
	}
	return fmt.Sprintf(`"result":{"context":{"slot":%d},"value":[%s]}`, slot, strings.Join(values, ","))
}

func (n *stateNode) set(account, value string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.accounts[account] = value
}

// systemAccount and tokenAccount are getMultipleAccounts values: a wallet with its
// lamports, and a token account whose data slice is its amount (8 bytes, base64).
func systemAccount(lamports uint64) string {
	return fmt.Sprintf(`{"lamports":%d,"owner":"11111111111111111111111111111111","data":["","base64"],"executable":false,"rentEpoch":0,"space":0}`, lamports)
}

func tokenAccount(amountBase64 string) string {
	return fmt.Sprintf(`{"lamports":2039280,"owner":"%s","data":["%s","base64"],"executable":false,"rentEpoch":0,"space":165}`,
		solana.TokenProgramID, amountBase64)
}

func newKeys(n int) []string {
	keys := make([]string, n)
	for i := range keys {
		keys[i] = solana.NewWallet().PublicKey().String()
	}
	return keys
}

func TestSolana_AccountStates_ReadsAHundredAccountsACallAtTheLowestSlotAnswered(t *testing.T) {
	keys := newKeys(150)
	state := &stateNode{accounts: map[string]string{keys[0]: systemAccount(10_000_000), keys[120]: tokenAccount("QEIPAAAAAAA=")},
		slots: []uint64{500, 480}}
	node := newMethodNode(t, func(req solanaRequest) string { return state.answer(t, req) })

	read, err := liveOn(node.server.URL).AccountStates(context.Background(), keys)

	require.NoError(t, err)
	calls := node.calls("getMultipleAccounts")
	require.Len(t, calls, 2, "a hundred accounts a call")
	var first []string
	calls[0].param(t, 0, &first)
	assert.Len(t, first, chain.AccountStateBatch)
	var options struct {
		Encoding, Commitment string
		DataSlice            struct{ Offset, Length int }
	}
	calls[0].param(t, 1, &options)
	assert.Equal(t, "base64", options.Encoding)
	assert.Equal(t, solanaCommitmentFinalized, options.Commitment)
	assert.Equal(t, 64, options.DataSlice.Offset, "a token account's amount, after its mint and owner")
	assert.Equal(t, 8, options.DataSlice.Length)

	assert.Equal(t, uint64(480), read.Slot, "the lowest slot a call was answered at")
	require.Len(t, read.States, 150)
	assert.Equal(t, "10000000/11111111111111111111111111111111/", read.States[keys[0]])
	assert.Equal(t, "2039280/"+solana.TokenProgramID.String()+"/QEIPAAAAAAA=", read.States[keys[120]])
	assert.Empty(t, read.States[keys[1]], "an account that does not exist has no state")
}

// A transfer moves an account's state: lamports for SOL, the amount for a token
// account, and an account opened by the transfer (an associated token account the
// sender creates) goes from none to one.
func TestSolana_AccountStates_ChangeWithEveryTransfer(t *testing.T) {
	keys := newKeys(3)
	state := &stateNode{accounts: map[string]string{keys[0]: systemAccount(5), keys[1]: tokenAccount("AQAAAAAAAAA=")}, slots: []uint64{100}}
	node := newMethodNode(t, func(req solanaRequest) string { return state.answer(t, req) })
	live := liveOn(node.server.URL)

	before, err := live.AccountStates(context.Background(), keys)
	require.NoError(t, err)
	state.set(keys[0], systemAccount(6))
	state.set(keys[1], tokenAccount("AgAAAAAAAAA="))
	state.set(keys[2], tokenAccount("AQAAAAAAAAA="))
	after, err := live.AccountStates(context.Background(), keys)
	require.NoError(t, err)

	for _, key := range keys {
		assert.NotEqual(t, before.States[key], after.States[key], "account %s", key)
	}
}

func TestSolana_AccountStates_RefusesAnAnswerThatDoesNotMatchTheAccounts(t *testing.T) {
	node := newMethodNode(t, func(solanaRequest) string {
		return `"result":{"context":{"slot":100},"value":[null]}`
	})

	_, err := liveOn(node.server.URL).AccountStates(context.Background(), newKeys(2))

	require.Error(t, err, "two accounts asked, one answered")
}

func TestSolana_AccountStates_RefusesAnAccountThatIsNoPublicKey(t *testing.T) {
	node := newMethodNode(t, func(solanaRequest) string { return `"result":{"context":{"slot":1},"value":[]}` })

	_, err := liveOn(node.server.URL).AccountStates(context.Background(), []string{"not-a-key"})

	require.Error(t, err)
	assert.Empty(t, node.calls("getMultipleAccounts"), "nothing is asked for an invalid account")
}

// The derived accounts are the owner and its associated accounts: no listing, and
// each mint's program is read once.
func TestSolana_DerivedAccounts_AreTheOwnerAndItsAssociatedAccountsWithoutAListing(t *testing.T) {
	node := mintNode(t, nil)
	live := liveOn(node.server.URL, devnetUSDC, token2022Token)

	got, err := live.DerivedAccounts(context.Background(), ataFixtureOwner)
	require.NoError(t, err)
	_, err = live.DerivedAccounts(context.Background(), ataFixtureOwner)
	require.NoError(t, err)

	assert.Equal(t, []string{ataFixtureOwner, ataFixtureToken2022, ataFixtureUSDC}, got)
	assert.Empty(t, node.calls("getTokenAccountsByOwner"))
	assert.Len(t, node.calls("getAccountInfo"), 2, "each mint's program is read once, then cached")
	listed, err := live.SignatureAccounts(context.Background(), ataFixtureOwner)
	require.NoError(t, err)
	assert.Equal(t, got, listed.Accounts[:len(got)], "SignatureAccounts starts with the same accounts")
}
