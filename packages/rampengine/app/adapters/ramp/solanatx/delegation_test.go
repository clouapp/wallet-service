package solanatx

import (
	"crypto/ed25519"
	"encoding/binary"
	"math"
	"testing"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/programs/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The sandbox values BlindPay's prepare answered with: the USDB test mint and
// the delegate its funding approves. They are public addresses; the adapter
// reads them from settings, never from code.
var (
	sandboxMint     = solana.MustPublicKeyFromBase58("oJSVoRMCyyPg8Y5TNt6t6o5NmVdRiwtis3B8mjZmUpc")
	sandboxDelegate = solana.MustPublicKeyFromBase58("AD7Gb17nRVZ9CnCykBmiwj8DNfVMksUVBGzCU8SzeYy6")
)

const (
	quoteAmount   = uint64(10_120_000) // "10.12" at 6 decimals
	quoteDecimals = uint8(6)
)

// delegationOwner is a fixed key, so the fuzz corpus keeps meaning the same
// owner between runs.
func delegationOwner() solana.PrivateKey {
	seed := make([]byte, ed25519.SeedSize)
	copy(seed, "xip delegation test owner")
	return solana.PrivateKey(ed25519.NewKeyFromSeed(seed))
}

func ata(t testing.TB, owner, mint, program solana.PublicKey) solana.PublicKey {
	t.Helper()
	address, _, err := solana.FindProgramAddress([][]byte{owner[:], program[:], mint[:]}, solana.SPLAssociatedTokenAccountProgramID)
	require.NoError(t, err)
	return address
}

// delegationTerms are the terms the sandbox quote carries.
func delegationTerms() DelegationTerms {
	return DelegationTerms{
		Owner:        delegationOwner().PublicKey(),
		Mint:         sandboxMint,
		Delegate:     sandboxDelegate,
		TokenProgram: solana.TokenProgramID,
		Amount:       quoteAmount,
		Decimals:     quoteDecimals,
	}
}

// delegationSpec is what a generated delegation transaction holds; each
// refusal case changes one thing of the valid one.
type delegationSpec struct {
	version  solana.MessageVersion
	feePayer solana.PublicKey
	program  solana.PublicKey
	source   solana.PublicKey
	mint     solana.PublicKey
	delegate solana.PublicKey
	owner    solana.PublicKey
	data     []byte
	extra    []solana.Instruction
}

func approveData(amount uint64, decimals uint8) []byte {
	data := []byte{approveCheckedTag}
	data = binary.LittleEndian.AppendUint64(data, amount)
	return append(data, decimals)
}

func validSpec(t testing.TB, version solana.MessageVersion) delegationSpec {
	owner := delegationOwner().PublicKey()
	return delegationSpec{
		version:  version,
		feePayer: owner,
		program:  solana.TokenProgramID,
		source:   ata(t, owner, sandboxMint, solana.TokenProgramID),
		mint:     sandboxMint,
		delegate: sandboxDelegate,
		owner:    owner,
		data:     approveData(quoteAmount, quoteDecimals),
	}
}

// build compiles the spec the way the provider does: the owner signs, the
// source is writable, mint, delegate and program are read-only.
func (s delegationSpec) build(t testing.TB) *solana.Transaction {
	t.Helper()
	approve := solana.NewInstruction(s.program, solana.AccountMetaSlice{
		solana.Meta(s.source).WRITE(),
		solana.Meta(s.mint),
		solana.Meta(s.delegate),
		solana.Meta(s.owner).SIGNER(),
	}, s.data)
	builder := solana.NewTransactionBuilder().
		AddInstruction(approve).
		SetRecentBlockHash(fixedBlockhash).
		SetFeePayer(s.feePayer)
	for _, ix := range s.extra {
		builder = builder.AddInstruction(ix)
	}
	tx, err := builder.Build()
	require.NoError(t, err)
	tx.Message.SetVersion(s.version)
	return tx
}

// wire round-trips the transaction through the production parse.
func wire(t testing.TB, tx *solana.Transaction) *solana.Transaction {
	t.Helper()
	raw, err := tx.MarshalBinary()
	require.NoError(t, err)
	parsed, _, err := Unsigned(b64(raw))
	require.NoError(t, err)
	return parsed
}

func TestCheckDelegationAcceptsTheLegacyTransactionBlindPayPrepares(t *testing.T) {
	tx := wire(t, validSpec(t, solana.MessageVersionLegacy).build(t))

	// The shape the sandbox answered: one signer, header {1,0,3}, five keys.
	require.Equal(t, solana.MessageHeader{NumRequiredSignatures: 1, NumReadonlySignedAccounts: 0, NumReadonlyUnsignedAccounts: 3}, tx.Message.Header)
	require.Len(t, tx.Message.AccountKeys, 5)
	assert.NoError(t, CheckDelegation(tx, delegationTerms()))
}

func TestCheckDelegationAcceptsAV0Transaction(t *testing.T) {
	tx := wire(t, validSpec(t, solana.MessageVersionV0).build(t))

	require.Equal(t, solana.MessageVersionV0, tx.Message.GetVersion())
	assert.NoError(t, CheckDelegation(tx, delegationTerms()))
}

func TestCheckDelegationAcceptsTheLibrarysApproveChecked(t *testing.T) {
	// The hand parse reads what solana-go's own builder writes.
	owner := delegationOwner().PublicKey()
	approve, err := token.NewApproveCheckedInstruction(
		quoteAmount, quoteDecimals,
		ata(t, owner, sandboxMint, solana.TokenProgramID), sandboxMint, sandboxDelegate, owner, nil,
	).ValidateAndBuild()
	require.NoError(t, err)
	tx, err := solana.NewTransactionBuilder().AddInstruction(approve).SetRecentBlockHash(fixedBlockhash).SetFeePayer(owner).Build()
	require.NoError(t, err)

	assert.NoError(t, CheckDelegation(wire(t, tx), delegationTerms()))
}

func TestCheckDelegationAcceptsToken2022WhenTheTermsSaySo(t *testing.T) {
	spec := validSpec(t, solana.MessageVersionLegacy)
	spec.program = solana.Token2022ProgramID
	spec.source = ata(t, spec.owner, sandboxMint, solana.Token2022ProgramID)
	terms := delegationTerms()
	terms.TokenProgram = solana.Token2022ProgramID

	assert.NoError(t, CheckDelegation(wire(t, spec.build(t)), terms))
}

func TestCheckDelegationRefuses(t *testing.T) {
	other := solana.NewWallet().PublicKey()
	owner := delegationOwner().PublicKey()
	cases := map[string]struct {
		change func(*delegationSpec)
		reason string
	}{
		"wrong mint": {func(s *delegationSpec) {
			s.mint = other
			s.source = ata(t, owner, other, solana.TokenProgramID)
		}, "source is not the owner's associated token account"},
		"wrong mint beside the right source": {func(s *delegationSpec) { s.mint = other }, "mint differs"},
		"wrong amount":                       {func(s *delegationSpec) { s.data = approveData(quoteAmount+1, quoteDecimals) }, "amount differs"},
		"wrong decimals":                     {func(s *delegationSpec) { s.data = approveData(quoteAmount, quoteDecimals+1) }, "decimals differ"},
		"wrong delegate":                     {func(s *delegationSpec) { s.delegate = other }, "delegate differs"},
		"source is not the owner's ATA":      {func(s *delegationSpec) { s.source = other }, "source is not the owner's associated token account"},
		"source is another owner's ATA":      {func(s *delegationSpec) { s.source = ata(t, other, sandboxMint, solana.TokenProgramID) }, "associated token account"},
		"source is the Token-2022 ATA": {func(s *delegationSpec) {
			s.source = ata(t, owner, sandboxMint, solana.Token2022ProgramID)
		}, "associated token account"},
		"another fee payer": {func(s *delegationSpec) { s.feePayer = other }, "requires 2 signatures"},
		"another authority": {func(s *delegationSpec) { s.owner = other }, "requires 2 signatures"},
		"extra Transfer instruction": {func(s *delegationSpec) {
			transfer := token.NewTransferInstruction(1, s.source, other, owner, nil).Build()
			s.extra = append(s.extra, transfer)
		}, "2 instructions"},
		"Token-2022 program for classic terms": {func(s *delegationSpec) {
			s.program = solana.Token2022ProgramID
			s.source = ata(t, owner, sandboxMint, solana.Token2022ProgramID)
		}, "not the token program"},
		"Approve, not ApproveChecked": {func(s *delegationSpec) {
			s.data = binary.LittleEndian.AppendUint64([]byte{4}, quoteAmount)
		}, "is 9 bytes"},
		"another tag of the same length": {func(s *delegationSpec) { s.data[0] = 12 }, "not ApproveChecked"},
		"trailing data byte":             {func(s *delegationSpec) { s.data = append(s.data, 0) }, "is 11 bytes"},
		"no data":                        {func(s *delegationSpec) { s.data = nil }, "is 0 bytes"},
		"memo program": {func(s *delegationSpec) {
			s.program = solana.MemoProgramID
		}, "not the token program"},
	}
	for _, version := range []solana.MessageVersion{solana.MessageVersionLegacy, solana.MessageVersionV0} {
		for name, c := range cases {
			t.Run(versionName(version)+"/"+name, func(t *testing.T) {
				spec := validSpec(t, version)
				c.change(&spec)

				err := CheckDelegation(wire(t, spec.build(t)), delegationTerms())

				require.ErrorIs(t, err, ErrDelegationRefused)
				assert.Contains(t, err.Error(), c.reason)
			})
		}
	}
}

func TestCheckDelegationRefusesTheOwnerAsASecondFeePayer(t *testing.T) {
	// Terms name an owner that is not the fee payer of a one-signer message.
	terms := delegationTerms()
	terms.Owner = solana.NewWallet().PublicKey()

	err := CheckDelegation(wire(t, validSpec(t, solana.MessageVersionLegacy).build(t)), terms)

	require.ErrorIs(t, err, ErrDelegationRefused)
	assert.Contains(t, err.Error(), "fee payer is not the owner")
}

func TestCheckDelegationRefusesAddressTableLookups(t *testing.T) {
	tx := validSpec(t, solana.MessageVersionV0).build(t)
	tx.Message.AddressTableLookups = solana.MessageAddressTableLookupSlice{{
		AccountKey:      solana.NewWallet().PublicKey(),
		WritableIndexes: []uint8{0},
	}}

	err := CheckDelegation(tx, delegationTerms())

	require.ErrorIs(t, err, ErrDelegationRefused)
	assert.Contains(t, err.Error(), "address table lookups")
}

func TestCheckDelegationRefusesMalformedCompiledInstructions(t *testing.T) {
	cases := map[string]struct {
		change func(*solana.Message)
		reason string
	}{
		"program index out of range": {func(m *solana.Message) {
			m.Instructions[0].ProgramIDIndex = uint16(len(m.AccountKeys))
		}, "program index is out of range"},
		"account index out of range": {func(m *solana.Message) {
			m.Instructions[0].Accounts[2] = math.MaxUint16
		}, "account 2 index is out of range"},
		"five accounts (a multisig signer)": {func(m *solana.Message) {
			m.Instructions[0].Accounts = append(m.Instructions[0].Accounts, 0)
		}, "5 accounts"},
		"three accounts": {func(m *solana.Message) {
			m.Instructions[0].Accounts = m.Instructions[0].Accounts[:3]
		}, "3 accounts"},
		"authority is an unsigned copy of the owner": {func(m *solana.Message) {
			m.AccountKeys = append(m.AccountKeys, m.AccountKeys[0])
			m.Instructions[0].Accounts[3] = uint16(len(m.AccountKeys) - 1)
		}, "not the signing owner"},
		"two required signatures": {func(m *solana.Message) {
			m.Header.NumRequiredSignatures = 2
		}, "requires 2 signatures"},
		"no instruction": {func(m *solana.Message) {
			m.Instructions = nil
		}, "0 instructions"},
		"no account keys": {func(m *solana.Message) {
			m.AccountKeys = nil
		}, "fee payer is not the owner"},
		"read-only signing owner": {func(m *solana.Message) {
			m.Header.NumReadonlySignedAccounts = 1
		}, "signing owner read-only"},
		"read-only source token account": {func(m *solana.Message) {
			// {1,0,3} leaves owner and source writable; {1,0,4} makes the
			// source read-only too.
			m.Header.NumReadonlyUnsignedAccounts = 4
		}, "source token account read-only"},
		"read-only count past the unsigned keys": {func(m *solana.Message) {
			m.Header.NumReadonlyUnsignedAccounts = uint8(len(m.AccountKeys))
		}, "source token account read-only"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			tx := validSpec(t, solana.MessageVersionLegacy).build(t)
			c.change(&tx.Message)

			err := CheckDelegation(tx, delegationTerms())

			require.ErrorIs(t, err, ErrDelegationRefused)
			assert.Contains(t, err.Error(), c.reason)
		})
	}
}

func TestCheckDelegationRefusesBlankTerms(t *testing.T) {
	tx := validSpec(t, solana.MessageVersionLegacy).build(t)
	cases := map[string]func(*DelegationTerms){
		"no owner":         func(d *DelegationTerms) { d.Owner = solana.PublicKey{} },
		"no mint":          func(d *DelegationTerms) { d.Mint = solana.PublicKey{} },
		"no delegate":      func(d *DelegationTerms) { d.Delegate = solana.PublicKey{} },
		"no token program": func(d *DelegationTerms) { d.TokenProgram = solana.PublicKey{} },
		"no amount":        func(d *DelegationTerms) { d.Amount = 0 },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			terms := delegationTerms()
			change(&terms)

			require.ErrorIs(t, CheckDelegation(tx, terms), ErrDelegationRefused)
		})
	}
	require.ErrorIs(t, CheckDelegation(nil, delegationTerms()), ErrDelegationRefused)
}

func TestCheckDelegationErrorsNameNoAddressNorAmount(t *testing.T) {
	// The adapter logs the reason; it must not restate the wallet or the value.
	spec := validSpec(t, solana.MessageVersionLegacy)
	spec.data = approveData(quoteAmount+1, quoteDecimals)

	err := CheckDelegation(wire(t, spec.build(t)), delegationTerms())

	require.Error(t, err)
	for _, secret := range []string{delegationOwner().PublicKey().String(), sandboxMint.String(), sandboxDelegate.String(), "10120000", "10120001"} {
		assert.NotContains(t, err.Error(), secret)
	}
}

func versionName(version solana.MessageVersion) string {
	if version == solana.MessageVersionV0 {
		return "v0"
	}
	return "legacy"
}

func TestBaseUnits(t *testing.T) {
	cases := []struct {
		amount   string
		decimals uint8
		want     uint64
	}{
		{"10.12", 6, 10_120_000},
		{"10", 6, 10_000_000},
		{"0.000001", 6, 1},
		{"10.123456", 6, 10_123_456},
		{"010.5", 2, 1050},
		{"0", 6, 0},
		{"7", 0, 7},
		{"18446744073709551615", 0, math.MaxUint64},
		{"18446744073709.551615", 6, math.MaxUint64},
	}
	for _, c := range cases {
		got, err := BaseUnits(c.amount, c.decimals)
		require.NoError(t, err, c.amount)
		assert.Equal(t, c.want, got, c.amount)
	}
}

func TestBaseUnitsRefuses(t *testing.T) {
	cases := []struct {
		amount   string
		decimals uint8
	}{
		{"10.1234567", 6},
		{"10.1200000", 6},
		{"1.5", 0},
		{"18446744073709551616", 0},
		{"18446744073709.551616", 6},
		{"18446744073710", 6},
		{"99999999999999999999999999", 6},
		{"-1", 6},
		{"-0.5", 6},
		{"+1", 6},
		{"", 6},
		{".", 6},
		{".5", 6},
		{"10.", 6},
		{"1e6", 6},
		{"1,5", 6},
		{"1.2.3", 6},
		{" 1", 6},
		{"1 ", 6},
		{"0x10", 6},
		{"١", 6},
	}
	for _, c := range cases {
		_, err := BaseUnits(c.amount, c.decimals)
		assert.Error(t, err, "%q at %d", c.amount, c.decimals)
	}
}
