package mpc

import (
	"context"
	"math/big"
)

// Curve identifies the elliptic curve used for MPC operations.
type Curve string

const (
	CurveSecp256k1 Curve = "secp256k1"
	CurveEd25519   Curve = "ed25519"
)

// KeygenResult holds the outputs of a 2-party MPC key generation ceremony.
type KeygenResult struct {
	ShareA         []byte // customer's share — must be encrypted before storage
	ShareB         []byte // service's share — must be sent to Secrets Manager
	CombinedPubKey []byte // compressed public key (33 bytes secp256k1; 32 bytes ed25519)
	ChainCode      []byte // 32-byte chain code for BIP-32/SLIP-0010 derivation
}

// SignInputs carries all transaction data required for signing.
// Bitcoin may have multiple UTXO inputs (one hash per input).
// ETH and Solana have a single hash.
type SignInputs struct {
	TxHashes [][]byte // one entry per input
	// KeyDerivationDelta signs for a BIP-32 child of the wallet key: the parties add
	// this tweak to their shares inside the ceremony, so the child key is never
	// assembled. Nil signs with the wallet key itself.
	KeyDerivationDelta *big.Int
	// ExpectedPublicKey is the compressed child public key the delta must produce;
	// it is required with KeyDerivationDelta and the ceremony refuses a mismatch.
	ExpectedPublicKey []byte
}

// Service is the MPC co-signing interface.
type Service interface {
	Keygen(ctx context.Context, curve Curve) (*KeygenResult, error)
	Sign(ctx context.Context, curve Curve, shareA, shareB []byte, inputs SignInputs) ([]byte, error)
	// ReconstructEd25519PrivateKey returns the SLIP-0010 master key that ed25519
	// child seeds derive from; it cannot sign for the wallet public key.
	ReconstructEd25519PrivateKey(shareA, shareB []byte) ([]byte, error)
	// ReconstructEd25519Scalar returns the scalar behind the wallet public key.
	ReconstructEd25519Scalar(shareA, shareB []byte) ([]byte, error)
	ReconstructSecp256k1PrivateKey(shareA, shareB []byte) ([]byte, error)
}
