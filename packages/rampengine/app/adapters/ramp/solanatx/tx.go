// Package solanatx parses and verifies the Solana transactions, legacy or v0,
// that a prepared off-ramp funding carries: the unsigned one a provider prepares and the
// signed one the wallet answers with. The parse is exact: the whole input is
// consumed and re-encodes to the same bytes, so the message the wallet signed
// is the message that was prepared, byte for byte. A versioned message other
// than v0 (first message byte 0x81 or above) is refused.
//
// solanatx is a chain codec any vendor adapter may share, not a vendor package.
package solanatx

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	bin "github.com/gagliardetto/binary"
	"github.com/gagliardetto/solana-go"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

// v0Prefix is the first message byte of a v0 message: the version bit (0x80)
// and version 0. A legacy message starts with its header instead, whose first
// byte (NumRequiredSignatures) has the version bit clear.
const v0Prefix = 0x80

// Unsigned decodes a base64 transaction that must be legacy or v0, carry
// header.NumRequiredSignatures signature slots all zero, and leave no byte
// unread. It answers the parsed transaction and the message bytes.
func Unsigned(b64 string) (*solana.Transaction, []byte, error) {
	tx, message, err := parse(b64)
	if err != nil {
		return nil, nil, err
	}
	for i, sig := range tx.Signatures {
		if !sig.IsZero() {
			return nil, nil, fmt.Errorf("solanatx: signature %d of an unsigned transaction is set", i)
		}
	}
	return tx, message, nil
}

// Fingerprint is the SHA-256 hex of the message bytes.
func Fingerprint(message []byte) string {
	sum := sha256.Sum256(message)
	return hex.EncodeToString(sum[:])
}

// VerifySigned decodes a base64 signed transaction and checks:
//   - legacy or v0;
//   - message bytes equal to the prepared ones, by fingerprint;
//   - signature count == header.NumRequiredSignatures;
//   - every signature non-zero and a valid Ed25519 signature of the message
//     by the account key at its index;
//   - no trailing bytes;
//   - re-encoding round-trips to the same bytes.
//
// Every failure wraps ramp.ErrInvalidSignedActionPayload.
func VerifySigned(b64, wantFingerprint string) (*solana.Transaction, error) {
	tx, message, err := parse(b64)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ramp.ErrInvalidSignedActionPayload, err)
	}
	if Fingerprint(message) != wantFingerprint {
		return nil, fmt.Errorf("%w: solanatx: message differs from the prepared one", ramp.ErrInvalidSignedActionPayload)
	}
	for i, sig := range tx.Signatures {
		if sig.IsZero() {
			return nil, fmt.Errorf("%w: solanatx: signature %d is missing", ramp.ErrInvalidSignedActionPayload, i)
		}
		key := tx.Message.AccountKeys[i]
		if !ed25519.Verify(ed25519.PublicKey(key[:]), message, sig[:]) {
			// The key is an address: the error names the index only.
			return nil, fmt.Errorf("%w: solanatx: signature %d does not verify by its account key", ramp.ErrInvalidSignedActionPayload, i)
		}
	}
	return tx, nil
}

// parse decodes a base64 legacy or v0 transaction exactly. It accepts only strict std
// base64 with no line break, so a transaction has one accepted spelling. It
// refuses an input over ramp.MaxUnsignedTransactionBytes before decoding it,
// a transaction that is neither legacy nor v0, one that leaves bytes unread or does not
// re-encode to the same bytes, and one whose signature count is not
// header.NumRequiredSignatures.
// It answers the transaction and its message bytes, as they were on the wire.
func parse(b64 string) (*solana.Transaction, []byte, error) {
	if len(b64) > base64.StdEncoding.EncodedLen(ramp.MaxUnsignedTransactionBytes) {
		return nil, nil, fmt.Errorf("solanatx: transaction exceeds %d bytes", ramp.MaxUnsignedTransactionBytes)
	}
	if strings.ContainsAny(b64, "\r\n") {
		return nil, nil, errors.New("solanatx: transaction base64 contains a line break")
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(b64)
	if err != nil {
		return nil, nil, fmt.Errorf("solanatx: transaction is not base64: %w", err)
	}
	if len(raw) == 0 {
		return nil, nil, errors.New("solanatx: transaction is empty")
	}
	if len(raw) > ramp.MaxUnsignedTransactionBytes {
		return nil, nil, fmt.Errorf("solanatx: transaction exceeds %d bytes", ramp.MaxUnsignedTransactionBytes)
	}
	tx, err := decodeExact(raw)
	if err != nil {
		return nil, nil, err
	}
	message, err := checkShape(tx, raw)
	if err != nil {
		return nil, nil, err
	}
	return tx, message, nil
}

// decodeExact decodes raw, requiring that every byte is consumed and that the
// transaction re-encodes to raw.
func decodeExact(raw []byte) (*solana.Transaction, error) {
	decoder := bin.NewBinDecoder(raw)
	tx, err := solana.TransactionFromDecoder(decoder)
	if err != nil {
		return nil, fmt.Errorf("solanatx: transaction does not decode: %w", err)
	}
	if decoder.Remaining() != 0 {
		return nil, fmt.Errorf("solanatx: transaction leaves %d bytes unread", decoder.Remaining())
	}
	encoded, err := tx.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("solanatx: transaction does not re-encode: %w", err)
	}
	if !bytes.Equal(encoded, raw) {
		return nil, errors.New("solanatx: transaction does not re-encode to the same bytes")
	}
	return tx, nil
}

// checkShape requires a legacy or v0 message without address table lookups,
// whose signature slots match its header and whose signers are static account
// keys, and answers the message bytes: the tail of raw after the signatures.
func checkShape(tx *solana.Transaction, raw []byte) ([]byte, error) {
	message, err := wireMessage(tx, raw)
	if err != nil {
		return nil, err
	}
	required := int(tx.Message.Header.NumRequiredSignatures)
	if len(tx.Message.AddressTableLookups) != 0 {
		return nil, errors.New("solanatx: transaction uses address table lookups")
	}
	if required == 0 {
		return nil, errors.New("solanatx: transaction requires no signature")
	}
	if len(tx.Signatures) != required {
		return nil, fmt.Errorf("solanatx: transaction carries %d signatures, header requires %d", len(tx.Signatures), required)
	}
	if required > len(tx.Message.AccountKeys) {
		return nil, fmt.Errorf("solanatx: header requires %d signatures but the message has %d account keys", required, len(tx.Message.AccountKeys))
	}
	return message, nil
}

// wireMessage answers the message bytes as they are on the wire, the tail of
// raw, once they equal the re-encoded message and their first byte agrees with
// the decoded version: a legacy header (version bit clear) or exactly the v0
// prefix.
func wireMessage(tx *solana.Transaction, raw []byte) ([]byte, error) {
	message, err := tx.Message.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("solanatx: message does not encode: %w", err)
	}
	if len(message) == 0 || len(message) > len(raw) {
		return nil, errors.New("solanatx: message bytes are not a legacy or v0 message")
	}
	wire := raw[len(raw)-len(message):]
	if !bytes.Equal(wire, message) {
		return nil, errors.New("solanatx: message bytes are not a legacy or v0 message")
	}
	switch version := tx.Message.GetVersion(); {
	case version == solana.MessageVersionLegacy && wire[0] < v0Prefix:
	case version == solana.MessageVersionV0 && wire[0] == v0Prefix:
	default:
		return nil, fmt.Errorf("solanatx: message version byte 0x%02x is neither legacy nor v0", wire[0])
	}
	return wire, nil
}
