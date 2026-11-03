package solanatx

import (
	"encoding/binary"
	"errors"
	"testing"

	bin "github.com/gagliardetto/binary"
	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/programs/token"
)

// delegationSeeds are the valid delegation, legacy and v0, and the refusal
// cases of the unit tests, as unsigned wire bytes.
func delegationSeeds(f *testing.F) [][]byte {
	f.Helper()
	other := solana.NewWallet().PublicKey()
	owner := delegationOwner().PublicKey()
	changes := []func(*delegationSpec){
		func(*delegationSpec) {},
		func(s *delegationSpec) { s.mint = other },
		func(s *delegationSpec) { s.data = approveData(quoteAmount+1, quoteDecimals) },
		func(s *delegationSpec) { s.data = approveData(quoteAmount, quoteDecimals+1) },
		func(s *delegationSpec) { s.delegate = other },
		func(s *delegationSpec) { s.source = other },
		func(s *delegationSpec) { s.feePayer = other },
		func(s *delegationSpec) {
			s.extra = append(s.extra, token.NewTransferInstruction(1, s.source, other, owner, nil).Build())
		},
		func(s *delegationSpec) {
			s.program = solana.Token2022ProgramID
			s.source = ata(f, owner, sandboxMint, solana.Token2022ProgramID)
		},
		func(s *delegationSpec) { s.data = append(s.data, 0) },
	}
	var raws [][]byte
	for _, version := range []solana.MessageVersion{solana.MessageVersionLegacy, solana.MessageVersionV0} {
		for _, change := range changes {
			spec := validSpec(f, version)
			change(&spec)
			raw, err := spec.build(f).MarshalBinary()
			if err != nil {
				f.Fatal(err)
			}
			raws = append(raws, raw)
		}
	}
	v0 := validSpec(f, solana.MessageVersionV0).build(f)
	raw, err := v0.MarshalBinary()
	if err != nil {
		f.Fatal(err)
	}
	withLookup := append(raw[:len(raw)-1:len(raw)-1], 1)
	withLookup = append(withLookup, other[:]...)
	withLookup = append(withLookup, 1, 0, 0)
	return append(raws, withLookup, nil)
}

func FuzzCheckDelegation(f *testing.F) {
	for _, raw := range delegationSeeds(f) {
		f.Add(raw, quoteAmount, quoteDecimals)
	}
	f.Fuzz(func(t *testing.T, raw []byte, amount uint64, decimals uint8) {
		terms := delegationTerms()
		terms.Amount = amount
		terms.Decimals = decimals
		// The production path: the strict parse, then the policy.
		if tx, _, err := Unsigned(b64(raw)); err == nil {
			checkFuzzedDelegation(t, tx, terms)
		}
		// The policy alone, on whatever the library decodes, so a shape the
		// strict parse refuses still reaches every bounds check.
		if tx, err := solana.TransactionFromDecoder(bin.NewBinDecoder(raw)); err == nil {
			checkFuzzedDelegation(t, tx, terms)
		}
	})
}

// checkFuzzedDelegation runs the policy and, when it accepts, restates the
// rules it must have enforced.
func checkFuzzedDelegation(t *testing.T, tx *solana.Transaction, terms DelegationTerms) {
	err := CheckDelegation(tx, terms)
	if err != nil {
		if !errors.Is(err, ErrDelegationRefused) {
			t.Fatalf("refusal does not wrap ErrDelegationRefused: %v", err)
		}
		return
	}
	msg := tx.Message
	if msg.Header.NumRequiredSignatures != 1 || len(msg.AddressTableLookups) != 0 || len(msg.Instructions) != 1 {
		t.Fatal("accepted a message of the wrong shape")
	}
	if !msg.AccountKeys[0].Equals(terms.Owner) {
		t.Fatal("accepted a fee payer that is not the owner")
	}
	ix := msg.Instructions[0]
	if msg.Header.NumReadonlySignedAccounts != 0 || int(ix.Accounts[0]) >= len(msg.AccountKeys)-int(msg.Header.NumReadonlyUnsignedAccounts) {
		t.Fatal("accepted a read-only owner or source")
	}
	if !msg.AccountKeys[ix.ProgramIDIndex].Equals(terms.TokenProgram) {
		t.Fatal("accepted another program")
	}
	data := ix.Data
	if len(data) != 10 || data[0] != 13 || binary.LittleEndian.Uint64(data[1:9]) != terms.Amount || data[9] != terms.Decimals {
		t.Fatal("accepted other instruction data")
	}
	source, _, err := solana.FindAssociatedTokenAddress(terms.Owner, terms.Mint)
	if err != nil {
		t.Fatal(err)
	}
	want := []solana.PublicKey{source, terms.Mint, terms.Delegate, terms.Owner}
	if len(ix.Accounts) != len(want) || ix.Accounts[3] != 0 {
		t.Fatal("accepted other instruction accounts")
	}
	for i, key := range want {
		if !msg.AccountKeys[ix.Accounts[i]].Equals(key) {
			t.Fatalf("accepted another account %d", i)
		}
	}
}
