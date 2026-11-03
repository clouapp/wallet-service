package solana

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"math/big"
	"testing"

	"github.com/gagliardetto/solana-go"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/stretchr/testify/require"
)

func swapTestAccount(owner string, lamports uint64, data []byte) *swapAccount {
	encoded, _ := json.Marshal(base64.StdEncoding.EncodeToString(data))
	encoding, _ := json.Marshal("base64")
	return &swapAccount{Owner: owner, Lamports: lamports, Data: []json.RawMessage{encoded, encoding}}
}

func swapTestTokenAccount(owner, mint solana.PublicKey, amount uint64, delegate bool) *swapAccount {
	data := make([]byte, 165)
	copy(data[:32], mint[:])
	copy(data[32:64], owner[:])
	binary.LittleEndian.PutUint64(data[64:72], amount)
	data[108] = 1
	if delegate {
		data[72] = 1
	}
	return swapTestAccount(solana.TokenProgramID.String(), 2_039_280, data)
}

func TestSwapSimulation_EnforcesNativeInputAndFeeBudget(t *testing.T) {
	sender := (solana.PublicKey{9}).String()
	keys := []string{sender}
	before := []*swapAccount{swapTestAccount(solana.SystemProgramID.String(), 1_000_000_000, nil)}
	for _, tc := range []struct {
		name      string
		remaining uint64
		allowed   bool
	}{{"quoted input and fee", 899_995_000, true}, {"unexpected additional native transfer", 800_000_000, false}, {"missing input debit", 999_995_000, false}} {
		t.Run(tc.name, func(t *testing.T) {
			after := []*swapAccount{swapTestAccount(solana.SystemProgramID.String(), tc.remaining, nil)}
			err := validateSwapAccounts(sender, big.NewInt(100_000_000), nil, keys, before, after, 5000)
			if tc.allowed {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestSwapSimulation_RejectsOtherTokenSpendsAndDelegationChanges(t *testing.T) {
	owner := solana.PublicKey{9}
	mint := solana.PublicKey{1}
	otherMint := solana.PublicKey{2}
	token := &types.Token{Contract: mint.String()}
	keys := []string{owner.String(), "source-token", "other-token"}
	before := []*swapAccount{swapTestAccount(solana.SystemProgramID.String(), 100_000_000, nil), swapTestTokenAccount(owner, mint, 2_000_000, false), swapTestTokenAccount(owner, otherMint, 5_000_000, false)}
	for _, tc := range []struct {
		name              string
		source, other     uint64
		delegate, allowed bool
	}{{"exact input", 1_000_000, 5_000_000, false, true}, {"additional source spend", 999_999, 5_000_000, false, false}, {"another token spent", 1_000_000, 4_000_000, false, false}, {"new delegation", 1_000_000, 5_000_000, true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			after := []*swapAccount{swapTestAccount(solana.SystemProgramID.String(), 99_995_000, nil), swapTestTokenAccount(owner, mint, tc.source, tc.delegate), swapTestTokenAccount(owner, otherMint, tc.other, false)}
			err := validateSwapAccounts(owner.String(), big.NewInt(1_000_000), token, keys, before, after, 5000)
			if tc.allowed {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
