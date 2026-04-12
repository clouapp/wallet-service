package wallet

import (
	"crypto/hmac"
	"crypto/sha512"
	"encoding/binary"
	"fmt"
)

type Ed25519ChildResult struct {
	ChildPrivateKey []byte // 32-byte ed25519 seed
	ChildChainCode  []byte // 32-byte chain code for further derivation
	Index           uint32
}

// deriveEd25519Child performs SLIP-0010 hardened child key derivation for ed25519.
// masterKey is the 32-byte ed25519 private key (scalar).
// chainCode is the 32-byte chain code from keygen.
// index is automatically hardened (0x80000000 is added).
func deriveEd25519Child(masterKey, chainCode []byte, index uint32) (*Ed25519ChildResult, error) {
	if len(masterKey) != 32 {
		return nil, fmt.Errorf("master key must be 32 bytes, got %d", len(masterKey))
	}
	if len(chainCode) != 32 {
		return nil, fmt.Errorf("chain code must be 32 bytes, got %d", len(chainCode))
	}

	hardenedIndex := 0x80000000 + index

	// SLIP-0010 data: 0x00 || ser256(kpar) || ser32(i)
	data := make([]byte, 1+32+4)
	data[0] = 0x00
	copy(data[1:33], masterKey)
	binary.BigEndian.PutUint32(data[33:], hardenedIndex)

	h := hmac.New(sha512.New, chainCode)
	h.Write(data)
	I := h.Sum(nil)

	childKey := make([]byte, 32)
	copy(childKey, I[:32])
	childChainCode := make([]byte, 32)
	copy(childChainCode, I[32:])

	return &Ed25519ChildResult{
		ChildPrivateKey: childKey,
		ChildChainCode:  childChainCode,
		Index:           index,
	}, nil
}
