package helius

import (
	"encoding/json"
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/macrowallets/waas/app/services/ingest/providers"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// VerifyInbound
// ---------------------------------------------------------------------------

func TestHelius_VerifyInbound_ValidAuthorization(t *testing.T) {
	provider := NewHeliusProvider("test-key")
	body := []byte(`[{"signature":"abc"}]`)
	secret := "Bearer test-secret-value"

	headers := providers.Header{}
	headers.Set("Authorization", secret)

	valid, err := provider.VerifyInbound(headers, body, secret)
	require.NoError(t, err)
	assert.True(t, valid)
}

func TestHelius_VerifyInbound_InvalidAuthorization(t *testing.T) {
	provider := NewHeliusProvider("test-key")
	body := []byte(`[]`)
	secret := "Bearer correct"

	headers := providers.Header{}
	headers.Set("Authorization", "Bearer wrong")

	valid, err := provider.VerifyInbound(headers, body, secret)
	require.NoError(t, err)
	assert.False(t, valid)
}

func TestHelius_VerifyInbound_MissingAuthorization(t *testing.T) {
	provider := NewHeliusProvider("test-key")
	body := []byte(`[]`)

	headers := providers.Header{}

	valid, err := provider.VerifyInbound(headers, body, "Bearer x")
	require.Error(t, err)
	assert.False(t, valid)
	assert.Contains(t, err.Error(), "missing Authorization")
}

// ---------------------------------------------------------------------------
// ParsePayload — credits read from accountData, as the deposit scanner reads them
// ---------------------------------------------------------------------------

const (
	heliusUSDCMint = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"
	heliusSig      = "5VERv8NMvzbJMEkV8xnrLkEaWRtSz9CosKDYjCJjBRnbJLgp8uirBgmQpjKhoR4tjF3ZpRzrFmBV6UjKdiSZkQUW"
)

func tokenChange(owner, tokenAccount, mint, amount string) map[string]interface{} {
	return map[string]interface{}{
		"userAccount": owner, "tokenAccount": tokenAccount, "mint": mint,
		"rawTokenAmount": map[string]interface{}{"tokenAmount": amount, "decimals": 6},
	}
}

func accountEntry(account string, lamports int64, tokens ...map[string]interface{}) map[string]interface{} {
	if tokens == nil {
		tokens = []map[string]interface{}{}
	}
	return map[string]interface{}{"account": account, "nativeBalanceChange": lamports, "tokenBalanceChanges": tokens}
}

func enhancedTx(t *testing.T, fields map[string]interface{}) []byte {
	t.Helper()
	tx := map[string]interface{}{"signature": heliusSig, "slot": 123456789, "timestamp": 1679000000, "transactionError": nil}
	for key, value := range fields {
		tx[key] = value
	}
	payload, err := json.Marshal([]map[string]interface{}{tx})
	require.NoError(t, err)
	return payload
}

func TestHelius_ParsePayload_CreditsLamportsAtTheAccountIndex(t *testing.T) {
	payload := enhancedTx(t, map[string]interface{}{"accountData": []map[string]interface{}{
		accountEntry("payer", -1_000_005_000),
		accountEntry("program", 0),
		accountEntry("receiver", 1_000_000_000),
	}})

	transfers, err := NewHeliusProvider("k").ParsePayload(payload)
	require.NoError(t, err)
	require.Len(t, transfers, 1, "the fee payer pays, it is not credited")

	tx := transfers[0]
	assert.Equal(t, heliusSig, tx.TxHash)
	assert.Equal(t, uint64(123456789), tx.BlockNumber)
	assert.Equal(t, "", tx.BlockHash)
	assert.Equal(t, "payer", tx.From)
	assert.Equal(t, "receiver", tx.To)
	assert.Equal(t, "sol", tx.Asset)
	assert.Equal(t, 2, tx.LogIndex, "a lamport credit is at the credited account's index")
	assert.Nil(t, tx.Token)
	assert.False(t, tx.AmountIsHuman)
	assert.Equal(t, 0, big.NewInt(1_000_000_000).Cmp(tx.Amount))
	assert.True(t, tx.Timestamp.Equal(time.Unix(1679000000, 0).UTC()))
}

func TestHelius_ParsePayload_CreditsATokenAfterTheAccountKeysInBaseUnits(t *testing.T) {
	payload := enhancedTx(t, map[string]interface{}{"accountData": []map[string]interface{}{
		accountEntry("payer", -5_000),
		accountEntry("ataSender", 0, tokenChange("sender", "ataSender", heliusUSDCMint, "-2200")),
		accountEntry("ataReceiver", 0, tokenChange("receiver", "ataReceiver", heliusUSDCMint, "1500")),
		accountEntry("ataReceiver2", 0, tokenChange("receiver", "ataReceiver2", heliusUSDCMint, "700")),
	}})

	transfers, err := NewHeliusProvider("k").ParsePayload(payload)
	require.NoError(t, err)
	require.Len(t, transfers, 1, "the receiver's two accounts are one credit")

	tx := transfers[0]
	assert.Equal(t, "sender", tx.From)
	assert.Equal(t, "receiver", tx.To)
	require.NotNil(t, tx.Token)
	assert.Equal(t, heliusUSDCMint, tx.Token.Contract)
	assert.Equal(t, "", tx.Token.Symbol, "the registry names the token")
	assert.False(t, tx.AmountIsHuman)
	assert.Equal(t, 0, big.NewInt(2_200).Cmp(tx.Amount))
	assert.Equal(t, 256+2, tx.LogIndex, "after every possible account index, at the lowest changed token account")
}

func TestHelius_ParsePayload_SkipsAFailedTransaction(t *testing.T) {
	payload := enhancedTx(t, map[string]interface{}{
		"transactionError": map[string]interface{}{"error": "custom program error: 0x1"},
		"accountData":      []map[string]interface{}{accountEntry("payer", -5_000), accountEntry("receiver", 10)},
	})

	transfers, err := NewHeliusProvider("k").ParsePayload(payload)
	require.NoError(t, err)
	assert.Empty(t, transfers, "a failed transaction only moves its fee")
}

func TestHelius_ParsePayload_SkipsATransactionItCannotPositionAndKeepsTheOthers(t *testing.T) {
	cases := map[string][]map[string]interface{}{
		"no account data": nil,
		"a token account missing from the accounts": {
			accountEntry("payer", -5_000),
			accountEntry("ataReceiver", 0, tokenChange("receiver", "elsewhere", heliusUSDCMint, "1500")),
		},
		"a token amount that is not base units": {
			accountEntry("payer", -5_000),
			accountEntry("ataReceiver", 0, tokenChange("receiver", "ataReceiver", heliusUSDCMint, "1.5")),
		},
		"a token change without its owner": {
			accountEntry("payer", -5_000),
			accountEntry("ataReceiver", 0, tokenChange("", "ataReceiver", heliusUSDCMint, "1500")),
		},
		"a token change listed twice": {
			accountEntry("payer", -5_000, tokenChange("receiver", "ataReceiver", heliusUSDCMint, "1500")),
			accountEntry("ataReceiver", 0, tokenChange("receiver", "ataReceiver", heliusUSDCMint, "1500")),
		},
	}
	for name, accounts := range cases {
		t.Run(name, func(t *testing.T) {
			bad := map[string]interface{}{"signature": "badSig", "slot": 7}
			if accounts != nil {
				bad["accountData"] = accounts
			}
			good := map[string]interface{}{"signature": heliusSig, "slot": 7, "accountData": []map[string]interface{}{
				accountEntry("payer", -1_005_000), accountEntry("receiver", 1_000_000),
			}}
			payload, err := json.Marshal([]map[string]interface{}{bad, good})
			require.NoError(t, err)

			transfers, err := NewHeliusProvider("k").ParsePayload(payload)
			require.NoError(t, err, "one transaction that cannot be read does not refuse the others")
			require.Len(t, transfers, 1)
			assert.Equal(t, heliusSig, transfers[0].TxHash)
		})
	}
}

func TestHelius_ParsePayload_RefusesABodyThatIsNotTransactions(t *testing.T) {
	_, err := NewHeliusProvider("k").ParsePayload([]byte(`{"not":"an array"}`))
	assert.Error(t, err)
}

// scannerFixture is the devnet slot 512232469 the Solana scanner tests read.
const scannerFixture = "../../chain/solana/testdata/solana/getBlock_v1_spl.json"

// heliusShapeOf rewrites a transaction of the recorded getBlock answer as Helius sends
// it: one accountData entry per account key, in key order, with its lamport change and
// the changes of the token balances it holds.
func heliusShapeOf(t *testing.T, signature string) []byte {
	t.Helper()
	raw, err := os.ReadFile(scannerFixture)
	require.NoError(t, err)
	type balance struct {
		AccountIndex  int    `json:"accountIndex"`
		Mint          string `json:"mint"`
		Owner         string `json:"owner"`
		UITokenAmount struct {
			Amount string `json:"amount"`
		} `json:"uiTokenAmount"`
	}
	var block struct {
		Result struct {
			Transactions []struct {
				Transaction struct {
					AccountKeys []struct {
						Pubkey string `json:"pubkey"`
					} `json:"accountKeys"`
					Signatures []string `json:"signatures"`
				} `json:"transaction"`
				Meta struct {
					PreBalances       []int64   `json:"preBalances"`
					PostBalances      []int64   `json:"postBalances"`
					PreTokenBalances  []balance `json:"preTokenBalances"`
					PostTokenBalances []balance `json:"postTokenBalances"`
				} `json:"meta"`
			} `json:"transactions"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(raw, &block))
	for _, tx := range block.Result.Transactions {
		if tx.Transaction.Signatures[0] != signature {
			continue
		}
		type holding struct {
			owner, mint string
		}
		changes := map[int]map[holding]*big.Int{}
		addBalance := func(balances []balance, sign int64) {
			for _, b := range balances {
				amount, ok := new(big.Int).SetString(b.UITokenAmount.Amount, 10)
				require.True(t, ok)
				if changes[b.AccountIndex] == nil {
					changes[b.AccountIndex] = map[holding]*big.Int{}
				}
				key := holding{b.Owner, b.Mint}
				if changes[b.AccountIndex][key] == nil {
					changes[b.AccountIndex][key] = new(big.Int)
				}
				changes[b.AccountIndex][key].Add(changes[b.AccountIndex][key], amount.Mul(amount, big.NewInt(sign)))
			}
		}
		addBalance(tx.Meta.PreTokenBalances, -1)
		addBalance(tx.Meta.PostTokenBalances, 1)
		var accounts []map[string]interface{}
		for i, key := range tx.Transaction.AccountKeys {
			var tokens []map[string]interface{}
			for h, delta := range changes[i] {
				if delta.Sign() != 0 { // Helius lists the balances that changed
					tokens = append(tokens, tokenChange(h.owner, key.Pubkey, h.mint, delta.String()))
				}
			}
			accounts = append(accounts, accountEntry(key.Pubkey, tx.Meta.PostBalances[i]-tx.Meta.PreBalances[i], tokens...))
		}
		return enhancedTx(t, map[string]interface{}{"signature": signature, "accountData": accounts})
	}
	t.Fatalf("transaction %s is not in %s", signature, scannerFixture)
	return nil
}

// The swap of devnet slot 512232469 that TestSolana_ScanBlock_CreditsRegisteredSPLTokensToTheTokenAccountOwner
// scans: the push of the same transaction finds the scanner's credits at the scanner's positions.
func TestHelius_ParsePayload_FindsTheScannersCreditsAtTheScannersPositions(t *testing.T) {
	const (
		swap      = "2kb1KrYNCc82UoDgAR742bysmxtYXrwBbgbTUVEPmQMb3udTR2ykDod6G7isQhp8fReVd2sWp3xmkr4t1A2bonsk"
		buyer     = "5uDDz3wNUz8YDafuEhS2ZELzsU6529dM9GS4LJrfNeoi"
		seller    = "vH9xX8SkhoNirTUw9UKsRiN78hQxmEVz3azsYpXbN7U"
		usdcMint  = "4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU"
		wsolMint  = "So11111111111111111111111111111111111111112"
		lamports1 = "8uBKcNhfn3DoaLU3ycmaunkEJ5zPU14masYoYE14KZ7"
	)
	transfers, err := NewHeliusProvider("k").ParsePayload(heliusShapeOf(t, swap))
	require.NoError(t, err)

	type credit struct {
		to, mint, amount string
		position         int
	}
	var got []credit
	for _, transfer := range transfers {
		mint := ""
		if transfer.Token != nil {
			mint = transfer.Token.Contract
		}
		got = append(got, credit{transfer.To, mint, transfer.Amount.String(), transfer.LogIndex})
	}
	// The scanner's credits of that transaction (scan_spl_test.go): lamports at 1, 4
	// and 6; USDC and wrapped SOL to the buyer at 259 and 260 (token accounts 3 and 4).
	assert.Subset(t, got, []credit{
		{lamports1, "", "5000", 1},
		{"5jt5jvW22vR6Hij4NWbJL9mZZzL3mgeGzcQhSdFECF4E", "", "6010", 4},
		{"955q2iVFTaJZR39F8j7QwqFZiVf5U9wFUfbDNWUenGqb", "", "1488440", 6},
		{buyer, usdcMint, "2200", 259},
		{buyer, wsolMint, "6010", 260},
	})
	for _, transfer := range transfers {
		if transfer.Token != nil && transfer.Token.Contract == usdcMint {
			assert.Equal(t, seller, transfer.From)
		}
	}
}
