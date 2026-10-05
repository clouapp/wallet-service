package chain

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/crypto"

	"github.com/macrowallets/waas/app/services/addressing"
	"github.com/macrowallets/waas/pkg/types"
)

const (
	tronPrivateKeySize = 32
	tronRSSize         = 64
	// java-tron accepts v as the recovery id (0/1, what wallet-cli, java-tron and
	// this adapter write) or as 27/28 (TronWeb), normalising v < 27 by adding 27.
	tronRecoveryIDOffset = 27
)

var (
	secp256k1Order     = crypto.S256().Params().N
	secp256k1HalfOrder = new(big.Int).Rsh(crypto.S256().Params().N, 1)
)

// unsignedTronRaw returns the raw data an unsigned transaction carries, checked
// against its txID (RawBytes), and its decoded form.
func unsignedTronRaw(unsigned *types.UnsignedTx) ([]byte, tronRawData, error) {
	if unsigned == nil || unsigned.Metadata == nil {
		return nil, tronRawData{}, fmt.Errorf("unsigned tron transaction metadata is required")
	}
	rawHex, ok := unsigned.Metadata["raw_data_hex"].(string)
	if !ok || rawHex == "" {
		return nil, tronRawData{}, fmt.Errorf("unsigned tron transaction has no raw_data_hex")
	}
	rawBytes, err := hex.DecodeString(rawHex)
	if err != nil {
		return nil, tronRawData{}, fmt.Errorf("unsigned tron transaction raw_data_hex: %w", err)
	}
	if len(unsigned.RawBytes) != tronTxIDSize || !bytes.Equal(unsigned.RawBytes, tronTxID(rawBytes)) {
		return nil, tronRawData{}, fmt.Errorf("unsigned tron transaction txID does not match its raw data")
	}
	raw, err := decodeTronRawData(rawBytes)
	if err != nil {
		return nil, tronRawData{}, err
	}
	return rawBytes, raw, nil
}

func tronSignedTx(chainID string, rawBytes, signature []byte) *types.SignedTx {
	return &types.SignedTx{
		ChainID:  chainID,
		TxHash:   hex.EncodeToString(tronTxID(rawBytes)),
		RawBytes: encodeTronTransaction(rawBytes, signature),
	}
}

// SignTransaction signs the txID with a local private key, which must own the
// transaction's owner_address.
func (a *TronLive) SignTransaction(ctx context.Context, unsigned *types.UnsignedTx, privateKey []byte) (*types.SignedTx, error) {
	rawBytes, raw, err := unsignedTronRaw(unsigned)
	if err != nil {
		return nil, err
	}
	if len(privateKey) != tronPrivateKeySize {
		return nil, fmt.Errorf("tron private key must be %d bytes", tronPrivateKeySize)
	}
	key, err := crypto.ToECDSA(privateKey)
	if err != nil {
		return nil, fmt.Errorf("tron private key: %w", err)
	}
	if !bytes.Equal(tronRawAddressOf(&key.PublicKey), raw.contract.owner) {
		return nil, fmt.Errorf("tron private key does not own the transaction's owner_address")
	}
	signature, err := crypto.Sign(unsigned.RawBytes, key)
	if err != nil {
		return nil, fmt.Errorf("tron sign: %w", err)
	}
	return tronSignedTx(unsigned.ChainID, rawBytes, signature), nil
}

// FinalizeMPCSignature turns the 64-byte R||S of the threshold ceremony over the
// txID into the broadcastable Transaction{raw_data, signature}. S is normalised to
// the lower half of the curve order and the recovery id (0/1) is the one whose
// recovered key is publicKey; any other key is refused.
func (a *TronLive) FinalizeMPCSignature(unsigned *types.UnsignedTx, signature []byte, publicKey []byte) (*types.SignedTx, error) {
	rawBytes, _, err := unsignedTronRaw(unsigned)
	if err != nil {
		return nil, err
	}
	if len(signature) != tronRSSize {
		return nil, fmt.Errorf("MPC signature must contain exactly %d R/S bytes", tronRSSize)
	}
	if len(publicKey) != 33 && len(publicKey) != 65 {
		return nil, fmt.Errorf("tron public key must contain 33 or 65 bytes")
	}
	expected, err := normalizeEVMCompressedPublicKey(publicKey)
	if err != nil {
		return nil, err
	}
	lowS, err := lowSSignature(signature)
	if err != nil {
		return nil, err
	}
	withRecovery, err := recoverEVMRecoveryID(unsigned.RawBytes, lowS, expected)
	if err != nil {
		return nil, err
	}
	return tronSignedTx(unsigned.ChainID, rawBytes, withRecovery), nil
}

// lowSSignature returns R||S with S replaced by N−S when it is in the upper half;
// R and S must be in [1, N−1].
func lowSSignature(signature []byte) ([]byte, error) {
	r := new(big.Int).SetBytes(signature[:32])
	s := new(big.Int).SetBytes(signature[32:tronRSSize])
	if r.Sign() <= 0 || r.Cmp(secp256k1Order) >= 0 || s.Sign() <= 0 || s.Cmp(secp256k1Order) >= 0 {
		return nil, fmt.Errorf("MPC signature R or S is out of range")
	}
	if s.Cmp(secp256k1HalfOrder) > 0 {
		s.Sub(secp256k1Order, s)
	}
	normalized := make([]byte, tronRSSize)
	r.FillBytes(normalized[:32])
	s.FillBytes(normalized[32:])
	return normalized, nil
}

// VerifySignedTransaction decodes the signed transaction, requires its raw data to
// be the built one byte for byte (so the txID is too) with exactly one signature,
// and recovers the signer, which must be `from` and the contract's owner_address.
// It runs after signing and before broadcast, independently of how the signature
// was produced.
func (a *TronLive) VerifySignedTransaction(unsigned *types.UnsignedTx, signed *types.SignedTx, from string) error {
	if unsigned == nil || signed == nil || len(signed.RawBytes) == 0 {
		return fmt.Errorf("tron verify: unsigned and signed transactions are required")
	}
	fromRaw, err := addressing.DecodeTronAddress(from)
	if err != nil {
		return fmt.Errorf("tron verify: source address: %w", err)
	}
	builtRaw, raw, err := unsignedTronRaw(unsigned)
	if err != nil {
		return fmt.Errorf("tron verify: %w", err)
	}
	signedRaw, signatures, err := decodeTronTransaction(signed.RawBytes)
	if err != nil {
		return fmt.Errorf("tron verify: %w", err)
	}
	if !bytes.Equal(signedRaw, builtRaw) {
		return fmt.Errorf("tron verify: signed transaction differs from the built one")
	}
	txID := tronTxID(signedRaw)
	if signed.TxHash != "" && !strings.EqualFold(signed.TxHash, hex.EncodeToString(txID)) {
		return fmt.Errorf("tron verify: tx hash %s is not the txID %x", signed.TxHash, txID)
	}
	if len(signatures) != 1 {
		return fmt.Errorf("tron verify: transaction carries %d signatures, want 1", len(signatures))
	}
	signer, err := recoverTronSigner(txID, signatures[0])
	if err != nil {
		return fmt.Errorf("tron verify: %w", err)
	}
	if !bytes.Equal(raw.contract.owner, fromRaw) {
		return fmt.Errorf("tron verify: owner_address %x is not %s", raw.contract.owner, from)
	}
	if !bytes.Equal(signer, fromRaw) {
		signerAddress, _ := addressing.EncodeTronAddress(signer)
		return fmt.Errorf("tron verify: signed by %s, expected %s", signerAddress, from)
	}
	return nil
}

// recoverTronSigner returns the 21-byte address whose key produced signature
// (r||s||v, v as recovery id or recovery id + 27) over txID.
func recoverTronSigner(txID, signature []byte) ([]byte, error) {
	if len(signature) != tronSignatureSize {
		return nil, fmt.Errorf("signature has %d bytes, want %d", len(signature), tronSignatureSize)
	}
	normalized := append([]byte(nil), signature...)
	if normalized[64] >= tronRecoveryIDOffset {
		normalized[64] -= tronRecoveryIDOffset
	}
	if normalized[64] > 1 {
		return nil, fmt.Errorf("signature recovery id %d is not 0 or 1", signature[64])
	}
	publicKey, err := crypto.SigToPub(txID, normalized)
	if err != nil {
		return nil, fmt.Errorf("recover signer: %w", err)
	}
	return tronRawAddressOf(publicKey), nil
}

func tronRawAddressOf(publicKey *ecdsa.PublicKey) []byte {
	return append([]byte{addressing.TronAddressPrefix}, crypto.PubkeyToAddress(*publicKey).Bytes()...)
}
