package solanatx

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/programs/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

// fixedBlockhash keeps the generated transactions independent of any cluster.
var fixedBlockhash = solana.MustHashFromBase58("4uQeVj5tqViQh7yWWGStvkEG1Zmhx6uasJtWCJziofM")

// fixture is a generated v0 transaction: an ApproveChecked of the owner's
// token account to a delegate, paid by feePayer.
type fixture struct {
	tx       *solana.Transaction
	signers  []solana.PrivateKey
	unsigned []byte
}

func newFixture(t testing.TB, version solana.MessageVersion, signers ...solana.PrivateKey) fixture {
	t.Helper()
	if len(signers) == 0 {
		signers = []solana.PrivateKey{solana.NewWallet().PrivateKey}
	}
	feePayer := signers[0].PublicKey()
	owner := signers[len(signers)-1].PublicKey()
	approve, err := token.NewApproveCheckedInstruction(
		1_500_000, 6,
		solana.NewWallet().PublicKey(), // source token account
		solana.NewWallet().PublicKey(), // mint
		solana.NewWallet().PublicKey(), // delegate
		owner,
		nil,
	).ValidateAndBuild()
	require.NoError(t, err)

	tx, err := solana.NewTransactionBuilder().
		AddInstruction(approve).
		SetRecentBlockHash(fixedBlockhash).
		SetFeePayer(feePayer).
		Build()
	require.NoError(t, err)
	// solana-go v1.14 builds legacy unless address tables are used; the
	// version is forced on the message.
	tx.Message.SetVersion(version)
	unsigned, err := tx.MarshalBinary()
	require.NoError(t, err)
	return fixture{tx: tx, signers: signers, unsigned: unsigned}
}

func (f fixture) message(t testing.TB) []byte {
	t.Helper()
	message, err := f.tx.Message.MarshalBinary()
	require.NoError(t, err)
	return message
}

// signedBy signs the fixture's transaction with keys, in place of its signers.
func (f fixture) signedBy(t testing.TB, keys ...solana.PrivateKey) []byte {
	t.Helper()
	tx := *f.tx
	tx.Signatures = nil
	message := f.message(t)
	for _, key := range keys {
		sig, err := key.Sign(message)
		require.NoError(t, err)
		tx.Signatures = append(tx.Signatures, sig)
	}
	raw, err := tx.MarshalBinary()
	require.NoError(t, err)
	return raw
}

func (f fixture) signed(t testing.TB) []byte {
	t.Helper()
	return f.signedBy(t, f.signers...)
}

func b64(raw []byte) string { return base64.StdEncoding.EncodeToString(raw) }

func TestUnsignedAcceptsAGeneratedV0Transaction(t *testing.T) {
	f := newFixture(t, solana.MessageVersionV0)

	tx, message, err := Unsigned(b64(f.unsigned))

	require.NoError(t, err)
	assert.Equal(t, f.message(t), message)
	assert.Equal(t, byte(0x80), message[0])
	assert.Len(t, tx.Signatures, 1)
	assert.Equal(t, f.tx.Message.AccountKeys, tx.Message.AccountKeys)
}

func TestUnsignedAcceptsTheLegacyTransactionBlindPayPrepares(t *testing.T) {
	// BlindPay's prepare answers a legacy message: one signer that is also
	// the fee payer, five static keys, no lookups, one ApproveChecked.
	f := newFixture(t, solana.MessageVersionLegacy)

	tx, message, err := Unsigned(b64(f.unsigned))

	require.NoError(t, err)
	assert.Equal(t, f.message(t), message)
	assert.Equal(t, byte(0x01), message[0], "a legacy message starts with NumRequiredSignatures")
	assert.Equal(t, solana.MessageVersionLegacy, tx.Message.GetVersion())
	assert.Equal(t, solana.MessageHeader{NumRequiredSignatures: 1, NumReadonlySignedAccounts: 0, NumReadonlyUnsignedAccounts: 3}, tx.Message.Header)
	assert.Len(t, tx.Message.AccountKeys, 5)
	assert.Equal(t, f.signers[0].PublicKey(), tx.Message.AccountKeys[0], "the owner pays the fee")
	assert.Len(t, tx.Message.Instructions, 1)
	assert.Empty(t, tx.Message.AddressTableLookups)
	assert.Len(t, tx.Signatures, 1)
	assert.True(t, tx.Signatures[0].IsZero())
}

func TestUnsignedAcceptsTwoSignatureSlots(t *testing.T) {
	f := newFixture(t, solana.MessageVersionV0, solana.NewWallet().PrivateKey, solana.NewWallet().PrivateKey)

	tx, _, err := Unsigned(b64(f.unsigned))

	require.NoError(t, err)
	assert.Len(t, tx.Signatures, 2)
}

func TestUnsignedRefuses(t *testing.T) {
	f := newFixture(t, solana.MessageVersionV0)
	legacy := newFixture(t, solana.MessageVersionLegacy)
	cases := map[string]string{
		"signed":                     b64(f.signed(t)),
		"trailing byte":              b64(append(bytes.Clone(f.unsigned), 0)),
		"extra signature":            b64(withSignatureSlots(t, f, 2)),
		"no signature slot":          b64(withSignatureSlots(t, f, 0)),
		"truncated":                  b64(f.unsigned[:len(f.unsigned)-1]),
		"oversized":                  b64(make([]byte, ramp.MaxUnsignedTransactionBytes+1)),
		"oversized base64":           b64(make([]byte, 2*ramp.MaxUnsignedTransactionBytes)) + "!",
		"invalid base64":             "not base64!",
		"url-safe base64":            base64.URLEncoding.EncodeToString(bytes.Repeat([]byte{0xfb, 0xff}, 64)),
		"empty":                      "",
		"message only":               b64(f.message(t)),
		"address table lookup":       b64(withAddressTableLookup(t, f)),
		"embedded newline":           withLineBreak(b64(f.unsigned), "\n"),
		"embedded carriage return":   withLineBreak(b64(f.unsigned), "\r\n"),
		"non-canonical padding":      withLoosePadding(t, f.unsigned),
		"v1 prefix 0x81":             b64(withMessagePrefix(f.unsigned, 1, 0x81)),
		"prefix 0xff":                b64(withMessagePrefix(f.unsigned, 1, 0xff)),
		"prefix 0x7f":                b64(withMessagePrefix(f.unsigned, 1, 0x7f)),
		"more signers than keys":     b64(withMoreSignersThanKeys(t, f, 0)),
		"valid but oversized":        b64(oversizedTransaction(t)),
		"no required signer":         b64(withoutRequiredSigner(t, f)),
		"non-canonical count":        b64(withNonCanonicalKeyCount(t, f)),
		"legacy with v0 bit":         b64(withPrefixedLegacyMessage(t, legacy)),
		"legacy signed":              b64(legacy.signed(t)),
		"legacy trailing byte":       b64(append(bytes.Clone(legacy.unsigned), 0)),
		"legacy truncated":           b64(legacy.unsigned[:len(legacy.unsigned)-1]),
		"legacy non-canonical count": b64(withNonCanonicalKeyCount(t, legacy)),
		"legacy header 0x7f":         b64(withMessagePrefix(legacy.unsigned, 1, 0x7f)),
		"legacy header 0x81":         b64(withMessagePrefix(legacy.unsigned, 1, 0x81)),
		"v0 prefix spelled 0x01":     b64(withMessagePrefix(f.unsigned, 1, 0x01)),
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			tx, message, err := Unsigned(input)

			require.Error(t, err)
			assert.Nil(t, tx)
			assert.Nil(t, message)
		})
	}
}

func TestFingerprintIsTheSHA256HexOfTheMessage(t *testing.T) {
	assert.Equal(t, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", Fingerprint(nil))
	assert.Equal(t, "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad", Fingerprint([]byte("abc")))
}

func TestVerifySignedAcceptsTheSignedPreparedTransaction(t *testing.T) {
	f := newFixture(t, solana.MessageVersionV0)
	_, message, err := Unsigned(b64(f.unsigned))
	require.NoError(t, err)

	tx, err := VerifySigned(b64(f.signed(t)), Fingerprint(message))

	require.NoError(t, err)
	require.NoError(t, tx.VerifySignatures())
	assert.Equal(t, f.tx.Message.AccountKeys, tx.Message.AccountKeys)
}

func TestVerifySignedAcceptsTheSignedLegacyTransaction(t *testing.T) {
	f := newFixture(t, solana.MessageVersionLegacy)
	_, message, err := Unsigned(b64(f.unsigned))
	require.NoError(t, err)

	tx, err := VerifySigned(b64(f.signed(t)), Fingerprint(message))

	require.NoError(t, err)
	require.NoError(t, tx.VerifySignatures())
	assert.Equal(t, solana.MessageVersionLegacy, tx.Message.GetVersion())
}

func TestVerifySignedAcceptsTwoSigners(t *testing.T) {
	f := newFixture(t, solana.MessageVersionV0, solana.NewWallet().PrivateKey, solana.NewWallet().PrivateKey)

	_, err := VerifySigned(b64(f.signed(t)), Fingerprint(f.message(t)))

	require.NoError(t, err)
}

func TestVerifySignedRefuses(t *testing.T) {
	f := newFixture(t, solana.MessageVersionV0)
	pair := newFixture(t, solana.MessageVersionV0, solana.NewWallet().PrivateKey, solana.NewWallet().PrivateKey)
	legacy := newFixture(t, solana.MessageVersionLegacy)
	fingerprint := Fingerprint(f.message(t))
	signed := f.signed(t)
	tampered := flipMessageByte(signed)
	lookupSigned := signedRaw(t, withAddressTableLookup(t, f), f.signers[0])
	legacyFingerprint := Fingerprint(legacy.message(t))
	legacySigned := legacy.signed(t)
	legacyTampered := flipMessageByte(legacySigned)

	cases := map[string]struct {
		input       string
		fingerprint string
	}{
		"message byte flipped":                   {b64(tampered), fingerprint},
		"message byte flipped, re-hashed":        {b64(tampered), Fingerprint(tampered[len(tampered)-len(f.message(t)):])},
		"another fingerprint":                    {b64(signed), Fingerprint([]byte("another message"))},
		"empty fingerprint":                      {b64(signed), ""},
		"signed by another key":                  {b64(f.signedBy(t, solana.NewWallet().PrivateKey)), fingerprint},
		"signature missing":                      {b64(f.unsigned), fingerprint},
		"second signature missing":               {b64(withZeroSignature(pair.signed(t), 1)), Fingerprint(pair.message(t))},
		"second signer swapped":                  {b64(pair.signedBy(t, pair.signers[0], solana.NewWallet().PrivateKey)), Fingerprint(pair.message(t))},
		"signers out of order":                   {b64(pair.signedBy(t, pair.signers[1], pair.signers[0])), Fingerprint(pair.message(t))},
		"extra signature":                        {b64(withSignatureSlots(t, f, 2)), fingerprint},
		"trailing byte":                          {b64(append(bytes.Clone(signed), 0)), fingerprint},
		"oversized":                              {b64(make([]byte, ramp.MaxUnsignedTransactionBytes+1)), fingerprint},
		"valid but oversized":                    {b64(oversizedTransaction(t)), fingerprint},
		"non-canonical count":                    {b64(withNonCanonicalKeyCount(t, f)), fingerprint},
		"more signers than keys":                 {b64(withMoreSignersThanKeys(t, f, 0x01)), Fingerprint(withMoreSignersThanKeys(t, f, 0x01)[1+64*(len(f.tx.Message.AccountKeys)+1):])},
		"v1 prefix 0x81":                         {b64(withMessagePrefix(signed, 1, 0x81)), Fingerprint(withMessagePrefix(signed, 1, 0x81)[65:])},
		"prefix 0x7f":                            {b64(withMessagePrefix(signed, 1, 0x7f)), Fingerprint(withMessagePrefix(signed, 1, 0x7f)[65:])},
		"address table lookup":                   {b64(lookupSigned), Fingerprint(lookupSigned[65:])},
		"embedded newline":                       {withLineBreak(b64(signed), "\n"), fingerprint},
		"non-canonical padding":                  {withLoosePadding(t, signed), fingerprint},
		"legacy signed by another key":           {b64(legacy.signedBy(t, solana.NewWallet().PrivateKey)), legacyFingerprint},
		"legacy message byte flipped":            {b64(legacyTampered), legacyFingerprint},
		"legacy message byte flipped, re-hashed": {b64(legacyTampered), Fingerprint(legacyTampered[65:])},
		"legacy signature missing":               {b64(legacy.unsigned), legacyFingerprint},
		"legacy trailing byte":                   {b64(append(bytes.Clone(legacySigned), 0)), legacyFingerprint},
		"legacy non-canonical count":             {b64(signedRaw(t, withNonCanonicalKeyCount(t, legacy), legacy.signers[0])), legacyFingerprint},
		"legacy fingerprint of the v0 message":   {b64(legacySigned), fingerprint},
		"legacy header 0x7f":                     {b64(withMessagePrefix(legacySigned, 1, 0x7f)), Fingerprint(withMessagePrefix(legacySigned, 1, 0x7f)[65:])},
		"invalid base64":                         {"not base64!", fingerprint},
		"empty":                                  {"", fingerprint},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			tx, err := VerifySigned(c.input, c.fingerprint)

			require.ErrorIs(t, err, ramp.ErrInvalidSignedActionPayload)
			assert.Nil(t, tx)
		})
	}
}

func TestVerifySignedRefusesASignatureOverTheReEncodedMessageOnly(t *testing.T) {
	// A signature over a message that is not the wire message must not pass,
	// even when the fingerprint is computed over what was signed.
	f := newFixture(t, solana.MessageVersionV0)
	other := newFixture(t, solana.MessageVersionV0, f.signers...)
	sig, err := f.signers[0].Sign(other.message(t))
	require.NoError(t, err)
	raw := bytes.Clone(f.unsigned)
	copy(raw[1:65], sig[:])

	_, err = VerifySigned(b64(raw), Fingerprint(f.message(t)))

	require.ErrorIs(t, err, ramp.ErrInvalidSignedActionPayload)
}

// oversizedTransaction is a well-formed v0 transaction over the size limit.
func oversizedTransaction(t testing.TB) []byte {
	t.Helper()
	payer := solana.NewWallet().PublicKey()
	memo := solana.NewInstruction(
		solana.MemoProgramID,
		solana.AccountMetaSlice{solana.Meta(payer).SIGNER()},
		bytes.Repeat([]byte{'x'}, ramp.MaxUnsignedTransactionBytes),
	)
	tx, err := solana.NewTransactionBuilder().
		AddInstruction(memo).
		SetRecentBlockHash(fixedBlockhash).
		SetFeePayer(payer).
		Build()
	require.NoError(t, err)
	tx.Message.SetVersion(solana.MessageVersionV0)
	raw, err := tx.MarshalBinary()
	require.NoError(t, err)
	decoded, err := solana.TransactionFromBytes(raw)
	require.NoError(t, err, "the library itself must take it, so only the limit refuses it")
	require.Equal(t, solana.MessageVersionV0, decoded.Message.GetVersion())
	return raw
}

// withoutRequiredSigner is the fixture's message with a header requiring no
// signature, and no signature slot.
func withoutRequiredSigner(t testing.TB, f fixture) []byte {
	t.Helper()
	message := bytes.Clone(f.message(t))
	message[1] = 0 // header.NumRequiredSignatures
	message[2] = 0 // header.NumReadonlySignedAccounts
	return append([]byte{0}, message...)
}

// withNonCanonicalKeyCount spells the fixture's account key count in two
// compact-u16 bytes where one suffices.
func withNonCanonicalKeyCount(t testing.TB, f fixture) []byte {
	t.Helper()
	message := f.message(t)
	at := 3 // legacy: the header, then the key count
	if message[0] == v0Prefix {
		at = 4
	}
	count := message[at]
	require.Less(t, count, byte(0x80))
	spelled := append(bytes.Clone(message[:at]), 0x80|count, 0x00)
	spelled = append(spelled, message[at+1:]...)
	return append(withSignatureSlots(t, f, 1)[:65], spelled...)
}

// withPrefixedLegacyMessage puts the v0 prefix byte in front of a legacy
// message body, which lacks the trailing lookup table count.
func withPrefixedLegacyMessage(t testing.TB, legacy fixture) []byte {
	t.Helper()
	raw := []byte{1}
	raw = append(raw, make([]byte, 64)...)
	raw = append(raw, v0Prefix)
	return append(raw, legacy.message(t)...)
}

// withMoreSignersThanKeys is the fixture's message with a header requiring one
// signature more than it has account keys, and that many slots filled with
// fill.
func withMoreSignersThanKeys(t testing.TB, f fixture, fill byte) []byte {
	t.Helper()
	message := bytes.Clone(f.message(t))
	required := len(f.tx.Message.AccountKeys) + 1
	require.Less(t, required, 0x80)
	message[1] = byte(required)
	raw := []byte{byte(required)}
	raw = append(raw, bytes.Repeat([]byte{fill}, 64*required)...)
	return append(raw, message...)
}

// withMessagePrefix replaces the version byte of a transaction carrying
// signatures signature slots. solana-go v1.14 decodes 0x81 and above as an
// unknown message version rather than refusing it.
func withMessagePrefix(raw []byte, signatures int, prefix byte) []byte {
	out := bytes.Clone(raw)
	out[1+64*signatures] = prefix
	return out
}

// withAddressTableLookup is the fixture's unsigned transaction with one
// address table lookup appended to its v0 message. The library decodes and
// re-encodes it exactly, so only the lookup check refuses it.
func withAddressTableLookup(t testing.TB, f fixture) []byte {
	t.Helper()
	raw := bytes.Clone(f.unsigned)
	require.Equal(t, byte(0), raw[len(raw)-1], "the fixture has no lookup")
	table := solana.NewWallet().PublicKey()
	raw = append(raw[:len(raw)-1], 1)
	raw = append(raw, table[:]...)
	raw = append(raw, 1, 0, 0) // one writable index (0), no readonly index
	tx, err := solana.TransactionFromBytes(raw)
	require.NoError(t, err)
	require.Len(t, tx.Message.AddressTableLookups, 1)
	encoded, err := tx.MarshalBinary()
	require.NoError(t, err)
	require.Equal(t, raw, encoded)
	return raw
}

// signedRaw fills the single signature slot of an unsigned transaction with
// key's signature over its message.
func signedRaw(t testing.TB, unsigned []byte, key solana.PrivateKey) []byte {
	t.Helper()
	require.Equal(t, byte(1), unsigned[0])
	sig, err := key.Sign(unsigned[65:])
	require.NoError(t, err)
	out := bytes.Clone(unsigned)
	copy(out[1:65], sig[:])
	return out
}

// withLineBreak puts a line break in the middle of a base64 string, which the
// lenient decoder skips.
func withLineBreak(encoded, lineBreak string) string {
	return encoded[:len(encoded)/2] + lineBreak + encoded[len(encoded)/2:]
}

// withLoosePadding spells raw in base64 with a padding bit set, which the
// lenient decoder accepts as the same bytes.
func withLoosePadding(t testing.TB, raw []byte) string {
	t.Helper()
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	encoded := []byte(b64(raw))
	last := bytes.IndexByte(encoded, '=') - 1
	require.Positive(t, last, "raw must need padding")
	encoded[last] = alphabet[strings.IndexByte(alphabet, encoded[last])^1]
	loose, err := base64.StdEncoding.DecodeString(string(encoded))
	require.NoError(t, err)
	require.Equal(t, raw, loose)
	return string(encoded)
}

// withSignatureSlots re-encodes the fixture's unsigned transaction with n zero
// signature slots, whatever its header requires.
func withSignatureSlots(t testing.TB, f fixture, n int) []byte {
	t.Helper()
	require.Less(t, n, 0x80)
	raw := []byte{byte(n)}
	raw = append(raw, make([]byte, 64*n)...)
	return append(raw, f.message(t)...)
}

func withZeroSignature(signed []byte, index int) []byte {
	out := bytes.Clone(signed)
	start := 1 + 64*index
	clear(out[start : start+64])
	return out
}

// flipMessageByte flips a byte of the recent blockhash inside the message.
func flipMessageByte(signed []byte) []byte {
	out := bytes.Clone(signed)
	at := bytes.Index(out, fixedBlockhash[:])
	out[at] ^= 0x01
	return out
}
